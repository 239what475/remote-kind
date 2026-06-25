package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/what/remote-kind/pkg/aliyun"
	"k8s.io/klog/v2"
)

// DeleteFromConfig reads a config file and deletes the named cluster.
func DeleteFromConfig(ctx context.Context, client *aliyun.Client, configPath string) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return DeleteByName(ctx, client, cfg.Name)
}

// DeleteByName discovers and deletes all resources for a cluster by name.
func DeleteByName(ctx context.Context, client *aliyun.Client, name string) error {
	klog.Infof("Deleting cluster %q", name)

	// 1. Delete instances, wait for ENI cleanup
	instanceIDs, err := client.ListInstancesByTag(aliyun.TagCluster, name)
	if err != nil {
		klog.Warningf("list instances: %v", err)
	}
	if len(instanceIDs) > 0 {
		klog.Infof("Deleting %d instances...", len(instanceIDs))
		if err := client.DeleteInstances(instanceIDs); err != nil {
			klog.Warningf("delete instances: %v", err)
		} else {
			ctxTerm, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			if err := client.WaitUntilTerminated(ctxTerm, instanceIDs); err != nil {
				klog.Warningf("wait terminated: %v", err)
			}
			klog.Infof("✓ Instances deleted")
		}
	}

	// 2. Delete VPC + sub-resources
	vpcName := fmt.Sprintf("remote-kind-%s", name)
	vpcs, _ := client.ListVPCsByName(vpcName)
	for _, vpcID := range vpcs {
		klog.Infof("Cleaning VPC %s...", vpcID)
		sgs, _ := client.ListSGsByVPC(vpcID)
		for _, sgID := range sgs {
			if err := client.DeleteSecurityGroup(sgID); err != nil {
				klog.Warningf("delete SG %s: %v", sgID, err)
			}
		}
		vsws, _ := client.ListVSwitchesByVPC(vpcID)
		for _, vswID := range vsws {
			if err := client.DeleteVSwitch(vswID); err != nil {
				klog.Warningf("delete vSwitch %s: %v", vswID, err)
			}
		}
		time.Sleep(10 * time.Second)
		for _, vswID := range vsws {
			if err := client.DeleteVSwitch(vswID); err != nil {
				klog.Warningf("delete vSwitch %s: %v", vswID, err)
			}
		}
		if err := client.DeleteVpc(vpcID); err != nil {
			klog.Warningf("delete VPC %s: %v", vpcID, err)
		} else {
			klog.Infof("✓ VPC deleted")
		}
	}

	klog.Infof("Cluster %q deleted", name)
	return nil
}
