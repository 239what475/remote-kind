package cluster

import (
	"context"
	"fmt"

	"github.com/239what475/remote-kind/pkg/aliyun"
	"k8s.io/klog/v2"
)

// ClusterSummary holds info for one cluster.
type ClusterSummary struct {
	Name      string
	Region    string
	NodeCount int
	CPIP      string
}

// NodeInfo holds info for one cluster node.
type NodeInfo struct {
	ID        string
	Name      string
	Role      string
	PrivateIP string
	PublicIP  string
	Status    string
}

// ListClusters discovers all remote-kind clusters in the region via tags.
func ListClusters(ctx context.Context, client *aliyun.Client) ([]ClusterSummary, error) {
	instances, err := client.ListAllInstances()
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}

	clusters := make(map[string]*ClusterSummary)
	for _, inst := range instances {
		clusterName := inst.Tags[aliyun.TagCluster]
		if clusterName == "" {
			continue
		}
		if _, ok := clusters[clusterName]; !ok {
			clusters[clusterName] = &ClusterSummary{
				Name:   clusterName,
				Region: client.Region,
			}
		}
		c := clusters[clusterName]
		c.NodeCount++
		if inst.Tags[aliyun.TagRole] == aliyun.RoleControlPlane && c.CPIP == "" {
			if inst.PublicIP != "" {
				c.CPIP = inst.PublicIP
			} else {
				c.CPIP = inst.PrivateIP
			}
		}
	}

	var result []ClusterSummary
	for _, c := range clusters {
		result = append(result, *c)
	}
	return result, nil
}

// ListNodes returns nodes for a specific cluster.
func ListNodes(ctx context.Context, client *aliyun.Client, clusterName string) ([]NodeInfo, error) {
	instances, err := client.ListAllInstances()
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}

	var result []NodeInfo
	for _, inst := range instances {
		if inst.Tags[aliyun.TagCluster] != clusterName {
			continue
		}
		role := inst.Tags[aliyun.TagRole]
		if role == "" {
			role = "unknown"
		}
		result = append(result, NodeInfo{
			ID:        inst.ID,
			Name:      inst.Name,
			Role:      role,
			PrivateIP: inst.PrivateIP,
			PublicIP:  inst.PublicIP,
			Status:    inst.Status,
		})
	}
	klog.V(2).Infof("Found %d nodes for cluster %q", len(result), clusterName)
	return result, nil
}
