package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/what/remote-kind/pkg/aliyun"
	"github.com/what/remote-kind/pkg/bootstrap"
	"k8s.io/klog/v2"
)

// ScaleWorkers adjusts the worker count for a cluster to the target number.
// It adds workers if target > current, or removes excess workers if target < current.
func ScaleWorkers(ctx context.Context, client *aliyun.Client, cfg *ClusterConfig, target int) error {
	name := cfg.Name
	if target < 0 {
		return fmt.Errorf("worker count must be >= 0, got %d", target)
	}

	// Discover existing nodes
	nodes, err := ListNodes(ctx, client, name)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}

	var workerIDs []string
	for _, n := range nodes {
		if n.Role == RoleWorker {
			workerIDs = append(workerIDs, n.ID)
		}
	}
	current := len(workerIDs)
	klog.Infof("Scaling workers %d → %d", current, target)

	if target == current {
		fmt.Printf("Already at %d workers, nothing to do.\n", current)
		return nil
	}

	// Find VPC/vSwitch from any existing instance (they share the same VPC)
	if current == 0 && target == 0 {
		return nil
	}
	// Pick any existing instance to get VSwitch and Security Group (they're shared)
	refID := getCPID(nodes)
	if len(workerIDs) > 0 {
		refID = workerIDs[0]
	}
	insts, err := client.GetInstanceInfo([]string{refID})
	if err != nil || len(insts) == 0 {
		return fmt.Errorf("no existing instance to infer network from")
	}
	vswitchID := insts[0].VSwitchID
	sgID := insts[0].SecurityGroupID

	// Find image from existing instance
	imageID, err := findImage(client)
	if err != nil {
		return err
	}

	workerSpec := cfg.Spec.Workers[0]

	if target > current {
		// Scale up: create + join new workers
		add := target - current
		return scaleUp(ctx, client, name, add, imageID, vswitchID, sgID, workerSpec)
	}

	// Scale down: delete excess workers
	remove := current - target
	cpID := getCPIDFromCluster(ctx, client, name)
	return scaleDown(ctx, client, cpID, workerIDs, remove)
}

func scaleUp(ctx context.Context, client *aliyun.Client, name string, count int, imageID, vswitchID, sgID string, workerSpec WorkerSpec) error {
	klog.Infof("Adding %d workers...", count)

	ci, err := bootstrap.GenerateCloudInit(&bootstrap.CloudInitData{Hostname: fmt.Sprintf("%s-w", name)})
	if err != nil {
		return fmt.Errorf("cloud-init: %w", err)
	}

	resp, err := client.RunInstances(&aliyun.RunInstanceInput{
		InstanceName:     fmt.Sprintf("%s-w", name),
		InstanceType:     workerSpec.InstanceType,
		ImageID:          imageID,
		VSwitchID:        vswitchID,
		SecurityGroupID:  sgID,
		UserData:         ci,
		SystemDiskSizeGB: DefaultSystemDiskSize,
		AssignPublicIP:   true,
		InstanceCount:    count,
		Tags:             map[string]string{aliyun.TagCluster: name, aliyun.TagRole: aliyun.RoleWorker},
	})
	if err != nil {
		return fmt.Errorf("create workers: %w", err)
	}
	ids := resp.InstanceIDs
	klog.Infof("Created %d workers: %v", len(ids), ids)

	wCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := client.WaitUntilRunning(wCtx, ids); err != nil {
		return fmt.Errorf("wait workers: %w", err)
	}
	if err := client.WaitForCloudAssistant(wCtx, ids); err != nil {
		return fmt.Errorf("wait ca: %w", err)
	}
	for _, wid := range ids {
		client.WaitForCloudInitTimeout(wCtx, wid, 2*time.Minute)
	}
	klog.Infof("Workers ready")

	// Get join command from control plane
	cpID := getCPIDFromCluster(ctx, client, name)
	if cpID == "" {
		return fmt.Errorf("no control plane found for cluster %q", name)
	}
	joinOut, err := client.RunCommand(ctx, cpID, "kubeadm token create --print-join-command", "token")
	if err != nil {
		return fmt.Errorf("join token: %w", err)
	}
	joinCmd := ""
	for line := range strings.SplitSeq(joinOut, "\n") {
		if len(line) > 0 && line[0:12] == "kubeadm join" {
			joinCmd = line
			break
		}
	}
	if joinCmd == "" {
		return fmt.Errorf("failed to parse join command from: %s", joinOut)
	}

	joinScript, err := bootstrap.RenderJoinScript(&bootstrap.JoinScriptData{JoinCommand: joinCmd})
	if err != nil {
		return fmt.Errorf("join script: %w", err)
	}
	ctxJ, cancelJ := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelJ()
	if _, err := client.RunCommandParallel(ctxJ, ids, joinScript, "kubeadm-join"); err != nil {
		return fmt.Errorf("join workers: %w", err)
	}
	klog.Infof("Workers joined: %d nodes", count)
	return nil
}

func scaleDown(ctx context.Context, client *aliyun.Client, cpID string, workerIDs []string, remove int) error {
	if remove <= 0 {
		return nil
	}
	toDelete := workerIDs[len(workerIDs)-remove:]
	klog.Infof("Removing %d workers: %v", len(toDelete), toDelete)

	// Drain nodes from K8s before deleting ECS
	ips := make([]string, len(toDelete))
	for i, id := range toDelete {
		ip, err := client.GetInstancePrivateIP(id)
		if err != nil {
			klog.Warningf("skip drain for %s: %v", id, err)
			continue
		}
		ips[i] = ip
	}
	drainScript := fmt.Sprintf(`#!/bin/bash
set -euo pipefail
for ip in %s; do
  node=$(kubectl get nodes -o wide --no-headers 2>/dev/null | awk -v ip="$ip" '$6==ip {print $1}')
  if [ -n "$node" ]; then
    echo "Draining $node..."
    kubectl drain "$node" --ignore-daemonsets --delete-emptydir-data --timeout=60s 2>&1 || true
    kubectl delete node "$node" 2>&1 || true
  fi
done
`, strings.Join(ips, " "))
	if cpID != "" {
		if _, err := client.RunCommand(ctx, cpID, drainScript, "drain-workers"); err != nil {
			klog.Warningf("drain workers: %v", err)
		}
	}

	if err := client.DeleteInstances(toDelete); err != nil {
		return fmt.Errorf("delete workers: %w", err)
	}
	ctxTerm, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := client.WaitUntilTerminated(ctxTerm, toDelete); err != nil {
		klog.Warningf("wait terminated: %v", err)
	}
	klog.Infof("Workers removed: %d", len(toDelete))
	return nil
}

func getCPID(nodes []NodeInfo) string {
	for _, n := range nodes {
		if n.Role == RoleControlPlane {
			return n.ID
		}
	}
	return ""
}

func getCPIDFromCluster(ctx context.Context, client *aliyun.Client, name string) string {
	nodes, err := ListNodes(ctx, client, name)
	if err != nil {
		klog.Warningf("list nodes: %v", err)
		return ""
	}
	return getCPID(nodes)
}
