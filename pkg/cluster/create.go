package cluster

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/what/remote-kind/pkg/aliyun"
	"github.com/what/remote-kind/pkg/bootstrap"
	"k8s.io/klog/v2"
)

//go:embed templates/*
var templateFS embed.FS

var kubeadmTmpl = template.Must(template.ParseFS(templateFS, "templates/kubeadm-config.yaml"))

// createdResources tracks cloud resources for rollback on failure.
type createdResources struct {
	vpcID     string
	vswitchID string
	sgID      string
	cpID      string
	workerIDs []string
}

// cleanup deletes all tracked resources in reverse order.
// Errors are logged but do not stop cleanup of remaining resources.
func (r *createdResources) cleanup(ctx context.Context, client *aliyun.Client) {
	if len(r.workerIDs) > 0 || r.cpID != "" {
		var allIDs []string
		if r.cpID != "" {
			allIDs = append(allIDs, r.cpID)
		}
		allIDs = append(allIDs, r.workerIDs...)
		if err := client.DeleteInstances(allIDs); err != nil {
			klog.Warningf("cleanup delete instances: %v", err)
		}
		cleanCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := client.WaitUntilTerminated(cleanCtx, allIDs); err != nil {
			klog.Warningf("cleanup wait terminated: %v", err)
		}
	}
	if r.sgID != "" {
		if err := client.DeleteSecurityGroup(r.sgID); err != nil {
			klog.Warningf("cleanup delete SG: %v", err)
		}
	}
	if r.vswitchID != "" {
		if err := client.DeleteVSwitch(r.vswitchID); err != nil {
			klog.Warningf("cleanup delete vSwitch: %v", err)
		}
	}
	if r.vpcID != "" {
		if err := client.DeleteVpc(r.vpcID); err != nil {
			klog.Warningf("cleanup delete VPC: %v", err)
		}
	}
}

func (c *ClusterState) Create(ctx context.Context, client *aliyun.Client) error {
	cfg := c.Config
	totalWorkers := 0
	for _, w := range cfg.Spec.Workers {
		totalWorkers += w.Replicas
	}
	klog.Infof("Creating cluster %q in %s/%s with %d workers", cfg.Name, cfg.Spec.Region, cfg.Spec.Zone, totalWorkers)

	var res createdResources
	var err error

	// On failure, clean up exactly what we created (not tag-based scanning).
	defer func() {
		if err != nil {
			klog.Warningf("Create failed, rolling back resources...")
			res.cleanup(context.Background(), client)
		}
	}()

	// ── Network ──
	vpcName := fmt.Sprintf("remote-kind-%s", cfg.Name)
	res.vpcID, err = client.CreateVpc(vpcName, DefaultVpcCIDR)
	if err != nil {
		return fmt.Errorf("vpc: %w", err)
	}
	c.VPCID = res.vpcID
	klog.V(2).Infof("VPC created: %s", res.vpcID)

	res.vswitchID, err = client.CreateVSwitch(vpcName, res.vpcID, cfg.Spec.Zone, DefaultVSwitchCIDR)
	if err != nil {
		return fmt.Errorf("vswitch: %w", err)
	}
	c.VSwitchID = res.vswitchID

	res.sgID, err = client.CreateSecurityGroup(vpcName, res.vpcID)
	if err != nil {
		return fmt.Errorf("sg: %w", err)
	}
	c.SecurityGroupID = res.sgID
	if err = client.AuthorizeSecurityGroupIngress(res.sgID, "22/22", "0.0.0.0/0", "TCP"); err != nil {
		return fmt.Errorf("sg rule ssh: %w", err)
	}
	if err = client.AuthorizeSecurityGroupIngress(res.sgID, "6443/6443", "0.0.0.0/0", "TCP"); err != nil {
		return fmt.Errorf("sg rule api: %w", err)
	}
	if err = client.AuthorizeSecurityGroupIngress(res.sgID, "30000/32767", "0.0.0.0/0", "TCP"); err != nil {
		return fmt.Errorf("sg rule nodeports: %w", err)
	}
	if err = client.AuthorizeSecurityGroupIngress(res.sgID, "-1/-1", DefaultVpcCIDR, "ALL"); err != nil {
		return fmt.Errorf("sg rule vpc: %w", err)
	}
	if err = client.AuthorizeSecurityGroupEgress(res.sgID, "-1/-1", "0.0.0.0/0", "ALL"); err != nil {
		return fmt.Errorf("sg rule egress: %w", err)
	}
	klog.V(2).Infof("Network ready: vsw=%s sg=%s", res.vswitchID, res.sgID)

	// ── SSH key ──
	sshKey, err := cfg.ReadSSHKey()
	if err != nil {
		return err
	}

	cpCI, err := bootstrap.GenerateCloudInit(&bootstrap.CloudInitData{Hostname: fmt.Sprintf("%s-cp", cfg.Name), SSHKey: sshKey})
	if err != nil {
		return fmt.Errorf("cp cloud-init: %w", err)
	}
	workerSpec := cfg.Spec.Workers[0]
	wCI, err := bootstrap.GenerateCloudInit(&bootstrap.CloudInitData{Hostname: fmt.Sprintf("%s-w", cfg.Name), SSHKey: sshKey})
	if err != nil {
		return fmt.Errorf("worker cloud-init: %w", err)
	}

	// ── Find image ──
	imageID, err := findImage(client)
	if err != nil {
		return err
	}

	// ── Launch CP + Workers in parallel ──
	type cpResult struct {
		id    string
		ip    string
		pubIP string
		err   error
	}
	type wResult struct {
		ids []string
		err error
	}
	cpCh := make(chan cpResult, 1)
	wCh := make(chan wResult, 1)

	go func() {
		resp, cpErr := client.RunInstances(&aliyun.RunInstanceInput{
			InstanceName: fmt.Sprintf("%s-cp", cfg.Name), InstanceType: cfg.Spec.ControlPlane.InstanceType,
			ImageID: imageID, VSwitchID: res.vswitchID, SecurityGroupID: res.sgID,
			UserData: cpCI, SystemDiskSizeGB: DefaultSystemDiskSize,
			AssignPublicIP: true, InstanceCount: 1,
			Tags: map[string]string{aliyun.TagCluster: cfg.Name, aliyun.TagRole: aliyun.RoleControlPlane},
		})
		if cpErr != nil {
			cpCh <- cpResult{err: fmt.Errorf("cp instance: %w", cpErr)}
			return
		}
		cpID := resp.InstanceIDs[0]
		klog.Infof("CP instance: %s", cpID)

		cpCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if cpErr = client.WaitUntilRunning(cpCtx, []string{cpID}); cpErr != nil {
			cpCh <- cpResult{err: fmt.Errorf("wait cp: %w", cpErr)}
			return
		}
		cpPubIP, err := client.GetInstancePublicIP(cpID)
		if err != nil {
			cpCh <- cpResult{err: fmt.Errorf("cp public IP: %w", err)}
			return
		}
		if cpErr = client.WaitForCloudAssistant(cpCtx, []string{cpID}); cpErr != nil {
			cpCh <- cpResult{err: fmt.Errorf("wait ca: %w", cpErr)}
			return
		}
		client.WaitForCloudInitTimeout(cpCtx, cpID, 3*time.Minute)
		cpIP, err := client.GetInstancePrivateIP(cpID)
		if err != nil {
			cpCh <- cpResult{err: fmt.Errorf("cp private IP: %w", err)}
			return
		}
		klog.Infof("CP ready: %s", cpIP)

		var kc bytes.Buffer
		if err := kubeadmTmpl.Execute(&kc, map[string]string{
			"KubernetesVersion": K8sVersion, "ImageRepository": "registry.k8s.io",
			"PodSubnet": cfg.Spec.Networking.PodSubnet, "ServiceSubnet": cfg.Spec.Networking.ServiceSubnet,
			"CertPubIP": cpPubIP,
		}); err != nil {
			cpCh <- cpResult{err: fmt.Errorf("kubeadm template: %w", err)}
			return
		}
		initScript, err := bootstrap.RenderInitScript(&bootstrap.InitScriptData{KubeadmConfig: kc.String()})
		if err != nil {
			cpCh <- cpResult{err: fmt.Errorf("init script: %w", err)}
			return
		}
		if _, cpErr = client.RunCommand(cpCtx, cpID, initScript, "kubeadm-init"); cpErr != nil {
			cpCh <- cpResult{err: fmt.Errorf("kubeadm init: %w", cpErr)}
			return
		}
		klog.Infof("kubeadm init done")

		joinOut, cpErr := client.RunCommand(cpCtx, cpID, "kubeadm token create --print-join-command", "token")
		if cpErr != nil {
			cpCh <- cpResult{err: fmt.Errorf("join token: %w", cpErr)}
			return
		}
		var joinCmd string
		for line := range strings.SplitSeq(joinOut, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "kubeadm join ") {
				joinCmd = strings.TrimSpace(line)
				break
			}
		}
		c.JoinCommand = joinCmd
		cpCh <- cpResult{id: cpID, ip: cpIP, pubIP: cpPubIP}
	}()

	go func() {
		resp, wErr := client.RunInstances(&aliyun.RunInstanceInput{
			InstanceName: fmt.Sprintf("%s-w", cfg.Name), InstanceType: workerSpec.InstanceType,
			ImageID: imageID, VSwitchID: res.vswitchID, SecurityGroupID: res.sgID,
			UserData: wCI, SystemDiskSizeGB: DefaultSystemDiskSize,
			AssignPublicIP: true, InstanceCount: workerSpec.Replicas,
			Tags: map[string]string{aliyun.TagCluster: cfg.Name, aliyun.TagRole: aliyun.RoleWorker},
		})
		if wErr != nil {
			wCh <- wResult{err: fmt.Errorf("workers: %w", wErr)}
			return
		}
		ids := resp.InstanceIDs
		klog.Infof("Workers created: %d", len(ids))

		wCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if wErr = client.WaitUntilRunning(wCtx, ids); wErr != nil {
			wCh <- wResult{err: fmt.Errorf("wait workers: %w", wErr)}
			return
		}
		if wErr = client.WaitForCloudAssistant(wCtx, ids); wErr != nil {
			wCh <- wResult{err: fmt.Errorf("wait ca: %w", wErr)}
			return
		}
		for _, wid := range ids {
			client.WaitForCloudInitTimeout(wCtx, wid, 2*time.Minute)
		}
		klog.Infof("Workers ready: %d", len(ids))
		wCh <- wResult{ids: ids}
	}()

	cpRes := <-cpCh
	if cpRes.err != nil {
		err = cpRes.err
		return err
	}
	res.cpID = cpRes.id
	c.ControlPlaneID = cpRes.id
	c.ControlPlaneIP = cpRes.ip
	c.ControlPlanePubIP = cpRes.pubIP

	wRes := <-wCh
	if wRes.err != nil {
		err = wRes.err
		return err
	}
	res.workerIDs = wRes.ids
	c.WorkerIDs = wRes.ids

	joinScript, err := bootstrap.RenderJoinScript(&bootstrap.JoinScriptData{JoinCommand: c.JoinCommand})
	if err != nil {
		c.JoinCommand = "" // will fail below
		err = fmt.Errorf("join script: %w", err)
		return err
	}
	ctxJ, cancelJ := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelJ()
	results, joinErr := client.RunCommandParallel(ctxJ, c.WorkerIDs, joinScript, "kubeadm-join")
	if joinErr != nil {
		var sb strings.Builder
		sb.WriteString(joinErr.Error())
		for id, out := range results {
			fmt.Fprintf(&sb, "\n[%s]: %s", id, out)
		}
		err = fmt.Errorf("join workers: %s", sb.String())
		return err
	}
	klog.Infof("All nodes joined: %d", 1+len(c.WorkerIDs))

	ctxKC, cancelKC := context.WithTimeout(ctx, 1*time.Minute)
	defer cancelKC()
	kc, err := client.RunCommand(ctxKC, c.ControlPlaneID, "cat /etc/kubernetes/admin.conf", "kubeconfig")
	if err != nil {
		return fmt.Errorf("kubeconfig: %w", err)
	}
	c.Kubeconfig = kc
	klog.Infof("Cluster %q ready", cfg.Name)
	return nil
}

// FlannelYAML returns the embedded kube-flannel.yml content.
func FlannelYAML() ([]byte, error) {
	return templateFS.ReadFile("templates/kube-flannel.yml")
}

// findImage returns the image ID for the configured image, or an error with
// a clear remediation message if it doesn't exist.
func findImage(client *aliyun.Client) (string, error) {
	img, err := client.FindImageByName(ImageName)
	if err != nil {
		return "", fmt.Errorf("lookup image %q: %w", ImageName, err)
	}
	if img == "" {
		return "", fmt.Errorf("image %q not found — run 'remote-kind build-image' first", ImageName)
	}
	return img, nil
}
