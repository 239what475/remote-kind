package cluster

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/klog/v2"

	"github.com/what/remote-kind/pkg/aliyun"
)

// ShowCluster queries cloud resources by tag and prints a detailed cluster summary.
func ShowCluster(ctx context.Context, client *aliyun.Client, name string) error {
	nodes, err := ListNodes(ctx, client, name)
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no cluster named %q found", name)
	}

	var cp NodeInfo
	var workers []NodeInfo
	for _, n := range nodes {
		switch n.Role {
		case RoleControlPlane:
			cp = n
		case RoleWorker:
			workers = append(workers, n)
		}
	}
	if cp.ID == "" {
		return fmt.Errorf("cluster %q has no control plane (orphaned workers?)", name)
	}

	fmt.Printf("Name:       %s\n", name)
	fmt.Printf("K8s:        %s\n", K8sVersion)
	fmt.Printf("Image:      %s\n", ImageName)
	fmt.Println()

	fmt.Println("Control Plane:")
	fmt.Printf("  ID:         %s\n", cp.ID)
	fmt.Printf("  Name:       %s\n", cp.Name)
	fmt.Printf("  Private IP: %s\n", cp.PrivateIP)
	fmt.Printf("  Public IP:  %s\n", cp.PublicIP)
	fmt.Printf("  Status:     %s\n", cp.Status)
	fmt.Println()

	if len(workers) > 0 {
		// Group workers by name prefix to count replicas per group
		groups := make(map[string]int)
		for _, w := range workers {
			key := strings.TrimRight(w.Name, "-0123456789")
			if key == "" {
				key = "default"
			}
			groups[key]++
		}
		if len(groups) == 1 {
			for k, v := range groups {
				fmt.Printf("Workers (%s × %d):\n", k, v)
			}
		} else {
			fmt.Println("Workers:")
		}
		fmt.Printf("  %-20s %-16s %-14s %-18s %s\n", "NAME", "ROLE", "PRIVATE_IP", "PUBLIC_IP", "STATUS")
		for _, w := range workers {
			fmt.Printf("  %-20s %-16s %-14s %-18s %s\n", w.Name, w.Role, w.PrivateIP, w.PublicIP, w.Status)
		}
	}

	// VPC info: lookup by VPC name pattern using tag-based instance discovery
	// We don't have VPC ID in NodeInfo, so we use the naming convention.
	vpcName := fmt.Sprintf("remote-kind-%s", name)
	vpcs, err := client.ListVPCsByName(vpcName)
	if err != nil {
		klog.Warningf("list VPCs: %v", err)
		return nil
	}
	if len(vpcs) > 0 {
		fmt.Println()
		fmt.Printf("VPC:        %s (%s)\n", vpcs[0], vpcName)
	}
	return nil
}
