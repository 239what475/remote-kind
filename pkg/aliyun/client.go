// Package aliyun provides a thin wrapper around Alibaba Cloud SDK v2 for
// creating and managing ECS, VPC, and Cloud Assistant resources.
package aliyun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	vpc "github.com/alibabacloud-go/vpc-20160428/v7/client"
)

// ── Tag constants ──

const (
	TagCluster       = "remote-kind-cluster"
	TagRole          = "remote-kind-role"
	RoleControlPlane = "control-plane"
	RoleWorker       = "worker"
)

// Client bundles the Alibaba Cloud service clients.
type Client struct {
	ECS    *ecs.Client
	VPC    *vpc.Client
	Region string
}

// NewClient creates authenticated Alibaba Cloud clients using credentials
// from ~/.aliyun/config.json (set up via 'aliyun configure').
// region must be provided (comes from remote-kind.yaml).
func NewClient(region string) (*Client, error) {
	ak, sk, _ := readAliyunCLIConfig()
	if ak == "" || sk == "" {
		return nil, fmt.Errorf("no credentials found — run 'aliyun configure'")
	}
	if region == "" {
		return nil, fmt.Errorf("region is required")
	}

	cfg := &openapi.Config{
		AccessKeyId:     &ak,
		AccessKeySecret: &sk,
		RegionId:        &region,
	}

	ecsCli, err := ecs.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("ecs: %w", err)
	}
	vpcCli, err := vpc.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("vpc: %w", err)
	}

	return &Client{ECS: ecsCli, VPC: vpcCli, Region: region}, nil
}

type aliyunCLIConfig struct {
	Current  string             `json:"current"`
	Profiles []aliyunCLIProfile `json:"profiles"`
}

type aliyunCLIProfile struct {
	Name            string `json:"name"`
	Mode            string `json:"mode"`
	AccessKeyID     string `json:"access_key_id"`
	AccessKeySecret string `json:"access_key_secret"`
	RegionID        string `json:"region_id"`
}

func readAliyunCLIConfig() (ak, sk, region string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", ""
	}
	configPath := filepath.Join(home, ".aliyun", "config.json")
	if !filepath.IsLocal(configPath) {
		return
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	var cfg aliyunCLIConfig
	if json.Unmarshal(data, &cfg) != nil {
		return
	}
	name := cfg.Current
	if name == "" {
		name = "default"
	}
	for _, p := range cfg.Profiles {
		if p.Name == name {
			return p.AccessKeyID, p.AccessKeySecret, p.RegionID
		}
	}
	return
}
