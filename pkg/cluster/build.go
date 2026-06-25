package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/239what475/remote-kind/pkg/aliyun"
	"github.com/239what475/remote-kind/pkg/bootstrap"
	"k8s.io/klog/v2"
)

// BuildImage creates a custom ECS image with pre-installed Kubernetes components.
// Set force=true to rebuild even if the image already exists.
// Set testMode=true to run kubeadm init for verification instead of creating the image.
func BuildImage(ctx context.Context, client *aliyun.Client, cfg *ClusterConfig, force, testMode bool) error {
	// Check if image already exists (before creating any cloud resources)
	existing, err := client.FindImageByName(ImageName)
	if err != nil {
		klog.Warningf("find existing image: %v", err)
	}
	if existing != "" && !force && !testMode {
		fmt.Printf("Image %s already exists (%s), skipping.\nUse --force to rebuild.\n", ImageName, existing)
		return nil
	}
	if force && existing != "" {
		if err := client.DeleteImage(existing); err != nil {
			klog.Warningf("delete old image: %v", err)
		}
	}

	lastDot := strings.LastIndex(K8sVersion, ".")
	k8sMinor := K8sVersion[:lastDot] // v1.36.2 → v1.36
	name := fmt.Sprintf("rk-build-%d", time.Now().Unix())
	mode := "build"
	if testMode {
		mode = "test"
	}
	fmt.Printf("=== Build Image: %s (%s mode) ===\n", name, mode)
	fmt.Printf("K8s: %s  Region: %s\n", K8sVersion, cfg.Spec.Region)

	// ── Network ──
	fmt.Print("[1/3] VPC+SG... ")
	vpcID, err := client.CreateVpc(name, DefaultVpcCIDR)
	if err != nil {
		return fmt.Errorf("vpc: %w", err)
	}
	vswID, err := client.CreateVSwitch(name, vpcID, cfg.Spec.Zone, DefaultVSwitchCIDR)
	if err != nil {
		return fmt.Errorf("vswitch: %w", err)
	}
	sgID, err := client.CreateSecurityGroup(name, vpcID)
	if err != nil {
		return fmt.Errorf("sg: %w", err)
	}
	defer func() {
		if err := client.DeleteSecurityGroup(sgID); err != nil {
			klog.Warningf("cleanup SG: %v", err)
		}
		// Retry vSwitch deletion — ENI may not be released yet
		for range 5 {
			time.Sleep(5 * time.Second)
			if err := client.DeleteVSwitch(vswID); err == nil {
				break
			}
		}
		if err := client.DeleteVpc(vpcID); err != nil {
			klog.Warningf("cleanup VPC: %v", err)
		}
	}()
	if err := client.AuthorizeSecurityGroupIngress(sgID, "-1/-1", DefaultVpcCIDR, "ALL"); err != nil {
		return fmt.Errorf("sg rule vpc: %w", err)
	}
	if err := client.AuthorizeSecurityGroupIngress(sgID, "22/22", "0.0.0.0/0", "TCP"); err != nil {
		return fmt.Errorf("sg rule ssh: %w", err)
	}
	if err := client.AuthorizeSecurityGroupEgress(sgID, "-1/-1", "0.0.0.0/0", "ALL"); err != nil {
		return fmt.Errorf("sg rule egress: %w", err)
	}
	fmt.Printf("%s %s %s\n", vpcID, vswID, sgID)

	// ── SSH key ──
	sshKey, err := cfg.ReadSSHKey()
	if err != nil {
		return err
	}
	ci, err := bootstrap.GenerateCloudInit(&bootstrap.CloudInitData{Hostname: "rk-build", SSHKey: sshKey})
	if err != nil {
		return fmt.Errorf("cloud-init: %w", err)
	}

	// ── ECS ──
	fmt.Print("[2/3] ECS (public IP)... ")
	resp, err := client.RunInstances(&aliyun.RunInstanceInput{
		InstanceName:     name,
		InstanceType:     cfg.Spec.ControlPlane.InstanceType,
		VSwitchID:        vswID,
		SecurityGroupID:  sgID,
		UserData:         ci,
		SystemDiskSizeGB: DefaultSystemDiskSize,
		AssignPublicIP:   true,
		InstanceCount:    1,
		Tags:             map[string]string{aliyun.TagCluster: name},
	})
	if err != nil {
		return fmt.Errorf("run instance: %w", err)
	}
	ecsID := resp.InstanceIDs[0]
	fmt.Println(ecsID)

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := client.WaitUntilRunning(waitCtx, []string{ecsID}); err != nil {
		return fmt.Errorf("wait running: %w", err)
	}
	if err := client.WaitForCloudAssistant(waitCtx, []string{ecsID}); err != nil {
		return fmt.Errorf("wait ca: %w", err)
	}
	pubIP, err := client.GetInstancePublicIP(ecsID)
	if err != nil {
		pubIP = "" // display only
	}
	fmt.Printf("ready (ssh root@%s)\n", pubIP)

	// ── Install ──
	fmt.Println("[3/3] Setup...")
	mirrors := map[string]string{}
	if cfg.Build != nil {
		mirrors = cfg.Build.Mirrors
	}
	installScript, err := bootstrap.RenderInstallScript(&bootstrap.InstallScriptData{
		Mirrors:         mirrors,
		K8sMinor:        k8sMinor,
		K8sVersion:      K8sVersion,
		FlannelImage:    FlannelImage,
		FlannelCNIImage: FlannelCNIImage,
	})
	if err != nil {
		return fmt.Errorf("install script: %w", err)
	}

	installCtx, cancel2 := context.WithTimeout(ctx, 25*time.Minute)
	defer cancel2()
	output, err := client.RunCommand(installCtx, ecsID, installScript, "install")
	if err != nil {
		return fmt.Errorf("setup failed:\n%s\n%w", output, err)
	}
	fmt.Println("ok")

	if testMode {
		initScript := fmt.Sprintf(`#!/bin/bash
set -euo pipefail
cat > /tmp/kubeadm-config.yaml << 'CONF'
apiVersion: %s
kind: ClusterConfiguration
kubernetesVersion: %s
imageRepository: registry.k8s.io
CONF
kubeadm init --config=/tmp/kubeadm-config.yaml 2>&1 | tee /tmp/kubeadm-init.log
mkdir -p $HOME/.kube
cp /etc/kubernetes/admin.conf $HOME/.kube/config
kubeadm token create --print-join-command
`, KubeadmAPIVersion, K8sVersion)
		testCtx, cancel3 := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel3()
		output, err = client.RunCommand(testCtx, ecsID, initScript, "kubeadm-init")
		if err != nil {
			return fmt.Errorf("kubeadm init failed:\n%s\n%w", output, err)
		}
		for line := range strings.SplitSeq(output, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "kubeadm join ") {
				fmt.Printf("\nJoin: %s\n", strings.TrimSpace(line))
			}
		}
		fmt.Println("\n=== TEST PASSED ===")
		fmt.Printf("Instance: %s\n", ecsID)
		return nil
	}

	// ── CreateImage ──
	fmt.Println("\nCreating image...")
	imageID, err := client.CreateImage(ecsID, ImageName)
	if err != nil {
		return fmt.Errorf("create image: %w", err)
	}
	fmt.Printf("Image ID: %s (name: %s)\n", imageID, ImageName)
	fmt.Print("Waiting for image... ")
	imgCtx, cancel4 := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel4()
	if err := client.WaitForImage(imgCtx, imageID); err != nil {
		return fmt.Errorf("wait for image: %w", err)
	}
	fmt.Println("ready")

	if err := client.DeleteInstances([]string{ecsID}); err != nil {
		klog.Warningf("cleanup build ECS: %v", err)
	}

	fmt.Println("\n=== SUCCESS ===")
	fmt.Printf("Image: %s\n", imageID)
	return nil
}
