package aliyun

import (
	"fmt"
	"time"

	vpc "github.com/alibabacloud-go/vpc-20160428/v7/client"
)

// ── VPC ──

func (c *Client) CreateVpc(name, cidr string) (string, error) {
	req := new(vpc.CreateVpcRequest)
	req.SetRegionId(c.Region)
	req.SetCidrBlock(cidr)
	req.SetVpcName(name)
	clusterTag := TagCluster
	req.SetTag([]*vpc.CreateVpcRequestTag{
		{Key: &clusterTag, Value: &name},
	})
	resp, err := c.VPC.CreateVpc(req)
	if err != nil {
		return "", fmt.Errorf("create vpc: %w", err)
	}
	vpcID := *resp.Body.VpcId

	// Wait for VPC to become Available before returning
	for range 30 {
		time.Sleep(2 * time.Second)
		desc, err := c.VPC.DescribeVpcs(&vpc.DescribeVpcsRequest{
			RegionId: &c.Region,
			VpcId:    &vpcID,
		})
		if err != nil {
			continue
		}
		for _, v := range desc.Body.Vpcs.Vpc {
			if v.Status != nil && *v.Status == "Available" {
				return vpcID, nil
			}
		}
	}
	return "", fmt.Errorf("timeout waiting for VPC %s", vpcID)
}

func (c *Client) DeleteVpc(vpcID string) error {
	req := new(vpc.DeleteVpcRequest)
	req.SetRegionId(c.Region)
	req.SetVpcId(vpcID)
	_, err := c.VPC.DeleteVpc(req)
	if err != nil {
		return fmt.Errorf("delete vpc %s: %w", vpcID, err)
	}
	return nil
}

// ── VSwitch ──

func (c *Client) CreateVSwitch(name, vpcID, zoneID, cidr string) (string, error) {
	req := new(vpc.CreateVSwitchRequest)
	req.SetRegionId(c.Region)
	req.SetZoneId(zoneID)
	req.SetVpcId(vpcID)
	req.SetCidrBlock(cidr)
	req.SetVSwitchName(name)
	resp, err := c.VPC.CreateVSwitch(req)
	if err != nil {
		return "", fmt.Errorf("create vswitch: %w", err)
	}
	vswID := *resp.Body.VSwitchId

	// Wait for vSwitch to become Available
	for range 30 {
		time.Sleep(2 * time.Second)
		desc, err := c.VPC.DescribeVSwitches(&vpc.DescribeVSwitchesRequest{
			RegionId:  &c.Region,
			VSwitchId: &vswID,
		})
		if err != nil {
			continue
		}
		for _, v := range desc.Body.VSwitches.VSwitch {
			if v.Status != nil && *v.Status == "Available" {
				return vswID, nil
			}
		}
	}
	return "", fmt.Errorf("timeout waiting for vSwitch %s", vswID)
}

// ListVPCsByName returns VPC IDs matching a name prefix.
func (c *Client) ListVPCsByName(name string) ([]string, error) {
	req := new(vpc.DescribeVpcsRequest)
	req.SetRegionId(c.Region)
	resp, err := c.VPC.DescribeVpcs(req)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, v := range resp.Body.Vpcs.Vpc {
		if v.VpcName != nil && *v.VpcName == name {
			ids = append(ids, *v.VpcId)
		}
	}
	return ids, nil
}

// ListVSwitchesByVPC returns vSwitch IDs in a VPC.
func (c *Client) ListVSwitchesByVPC(vpcID string) ([]string, error) {
	req := new(vpc.DescribeVSwitchesRequest)
	req.SetRegionId(c.Region)
	req.SetVpcId(vpcID)
	resp, err := c.VPC.DescribeVSwitches(req)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, v := range resp.Body.VSwitches.VSwitch {
		ids = append(ids, *v.VSwitchId)
	}
	return ids, nil
}

func (c *Client) DeleteVSwitch(vswitchID string) error {
	req := new(vpc.DeleteVSwitchRequest)
	req.SetRegionId(c.Region)
	req.SetVSwitchId(vswitchID)
	_, err := c.VPC.DeleteVSwitch(req)
	if err != nil {
		return fmt.Errorf("delete vswitch %s: %w", vswitchID, err)
	}
	return nil
}
