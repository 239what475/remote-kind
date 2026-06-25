package aliyun

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"time"

	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	"github.com/alibabacloud-go/tea/tea"
)

// ── Security Group ──

func (c *Client) CreateSecurityGroup(name, vpcID string) (string, error) {
	req := new(ecs.CreateSecurityGroupRequest)
	req.SetRegionId(c.Region)
	req.SetSecurityGroupName(name)
	req.SetVpcId(vpcID)
	resp, err := c.ECS.CreateSecurityGroup(req)
	if err != nil {
		return "", fmt.Errorf("create security group: %w", err)
	}
	return *resp.Body.SecurityGroupId, nil
}

func (c *Client) DeleteSecurityGroup(sgID string) error {
	req := new(ecs.DeleteSecurityGroupRequest)
	req.SetRegionId(c.Region)
	req.SetSecurityGroupId(sgID)
	_, err := c.ECS.DeleteSecurityGroup(req)
	if err != nil {
		return fmt.Errorf("delete security group %s: %w", sgID, err)
	}
	return nil
}

func (c *Client) AuthorizeSecurityGroupIngress(sgID, portRange, sourceCIDR, protocol string) error {
	req := new(ecs.AuthorizeSecurityGroupRequest)
	req.SetRegionId(c.Region)
	req.SetSecurityGroupId(sgID)
	req.SetIpProtocol(protocol)
	req.SetPortRange(portRange)
	req.SetSourceCidrIp(sourceCIDR)
	_, err := c.ECS.AuthorizeSecurityGroup(req)
	if err != nil {
		return fmt.Errorf("authorize ingress on %s: %w", sgID, err)
	}
	return nil
}

func (c *Client) AuthorizeSecurityGroupEgress(sgID, portRange, destCIDR, protocol string) error {
	req := new(ecs.AuthorizeSecurityGroupEgressRequest)
	req.SetRegionId(c.Region)
	req.SetSecurityGroupId(sgID)
	req.SetIpProtocol(protocol)
	req.SetPortRange(portRange)
	req.SetDestCidrIp(destCIDR)
	_, err := c.ECS.AuthorizeSecurityGroupEgress(req)
	if err != nil {
		return fmt.Errorf("authorize egress on %s: %w", sgID, err)
	}
	return nil
}

// ── ECS Instances ──

// RunInstanceInput holds parameters for creating ECS instances.
type RunInstanceInput struct {
	InstanceName     string
	HostName         string // OS hostname (default: instance ID)
	InstanceType     string
	ImageID          string // empty = Alibaba Cloud Linux 4 container optimized
	VSwitchID        string
	SecurityGroupID  string
	UserData         string // cloud-init script (raw, auto-base64-encoded)
	SystemDiskSizeGB int
	AssignPublicIP   bool
	Tags             map[string]string
	InstanceCount    int
}

// RunInstanceOutput holds the created instance IDs.
type RunInstanceOutput struct {
	InstanceIDs []string
}

func (c *Client) RunInstances(in *RunInstanceInput) (*RunInstanceOutput, error) {
	req := new(ecs.RunInstancesRequest)
	req.SetRegionId(c.Region)
	req.SetInstanceName(in.InstanceName)
	req.SetHostName(in.HostName)
	req.SetInstanceType(in.InstanceType)
	req.SetVSwitchId(in.VSwitchID)
	req.SetSecurityGroupId(in.SecurityGroupID)
	if in.InstanceCount > math.MaxInt32 {
		return nil, fmt.Errorf("instance count %d exceeds int32 max", in.InstanceCount)
	}
	req.SetAmount(int32(in.InstanceCount))

	// System disk: always set cloud_essd_entry (required for economy instance types).
	// The image default (cloud_essd) is not supported by all instance families.
	disk := new(ecs.RunInstancesRequestSystemDisk)
	disk.SetCategory("cloud_essd_entry")
	if in.SystemDiskSizeGB > 0 {
		disk.SetSize(fmt.Sprintf("%d", in.SystemDiskSizeGB))
	}
	req.SetSystemDisk(disk)

	// Image: use provided ID, or latest ACL4 container optimized
	imageID := in.ImageID
	if imageID == "" {
		var err error
		imageID, err = c.findLatestImage("acs:alibaba_cloud_linux_4_x64_container_optimized")
		if err != nil {
			return nil, fmt.Errorf("find image: %w", err)
		}
	}
	req.SetImageId(imageID)

	// User data (cloud-init)
	if in.UserData != "" {
		req.SetUserData(base64.StdEncoding.EncodeToString([]byte(in.UserData)))
	}

	// Network: no public IP by default
	if in.AssignPublicIP {
		req.SetInternetMaxBandwidthOut(int32(10)) // 10 Mbps, pay-by-traffic
	} else {
		req.SetInternetMaxBandwidthOut(int32(0))
	}

	// Tags
	if len(in.Tags) > 0 {
		var tags []*ecs.RunInstancesRequestTag
		for k, v := range in.Tags {
			tags = append(tags, &ecs.RunInstancesRequestTag{
				Key:   &k,
				Value: &v,
			})
		}
		req.SetTag(tags)
	}

	resp, err := c.ECS.RunInstances(req)
	if err != nil {
		return nil, fmt.Errorf("run instances: %w", err)
	}

	var ids []string
	for _, id := range resp.Body.InstanceIdSets.InstanceIdSet {
		ids = append(ids, *id)
	}
	return &RunInstanceOutput{InstanceIDs: ids}, nil
}

// WaitUntilRunning polls DescribeInstances until all given instances reach "Running".
func (c *Client) WaitUntilRunning(ctx context.Context, instanceIDs []string) error {
	idsJSON, err := json.Marshal(instanceIDs)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req := new(ecs.DescribeInstancesRequest)
	req.SetRegionId(c.Region)
	req.SetInstanceIds(string(idsJSON))

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}

		resp, err := c.ECS.DescribeInstances(req)
		if err != nil {
			continue
		}
		allRunning := true
		for _, inst := range resp.Body.Instances.Instance {
			if inst.Status == nil || *inst.Status != "Running" {
				allRunning = false
				break
			}
		}
		if allRunning {
			return nil
		}
	}
}

// findLatestImage returns the latest image for the given family.
func (c *Client) findLatestImage(family string) (string, error) {
	req := new(ecs.DescribeImagesRequest)
	req.SetRegionId(c.Region)
	req.SetStatus("Available")
	req.SetImageFamily(family)
	req.SetPageSize(int32(20))

	resp, err := c.ECS.DescribeImages(req)
	if err != nil {
		return "", fmt.Errorf("describe images: %w", err)
	}
	if len(resp.Body.Images.Image) == 0 {
		return "", fmt.Errorf("no image found for family %q", family)
	}
	return *resp.Body.Images.Image[0].ImageId, nil
}

// GetInstancePrivateIP returns the first private IP address of an instance.
func (c *Client) GetInstancePrivateIP(instanceID string) (string, error) {
	idsJSON, err := json.Marshal([]string{instanceID})
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	req := new(ecs.DescribeInstancesRequest)
	req.SetRegionId(c.Region)
	req.SetInstanceIds(string(idsJSON))

	resp, err := c.ECS.DescribeInstances(req)
	if err != nil {
		return "", fmt.Errorf("describe instance %s: %w", instanceID, err)
	}
	if len(resp.Body.Instances.Instance) == 0 {
		return "", fmt.Errorf("instance %s not found", instanceID)
	}
	inst := resp.Body.Instances.Instance[0]
	if len(inst.NetworkInterfaces.NetworkInterface) == 0 {
		return "", fmt.Errorf("instance %s has no network interfaces", instanceID)
	}
	if len(inst.NetworkInterfaces.NetworkInterface[0].PrivateIpSets.PrivateIpSet) == 0 {
		return "", fmt.Errorf("instance %s has no private IP", instanceID)
	}
	return *inst.NetworkInterfaces.NetworkInterface[0].PrivateIpSets.PrivateIpSet[0].PrivateIpAddress, nil
}

// InstanceInfo holds basic ECS instance details for listing.
type InstanceInfo struct {
	ID              string
	Name            string
	Status          string
	PrivateIP       string
	PublicIP        string
	VSwitchID       string
	SecurityGroupID string
	Tags            map[string]string
}

// GetInstanceInfo queries specific instances by ID.
func (c *Client) GetInstanceInfo(instanceIDs []string) ([]InstanceInfo, error) {
	idsJSON, err := json.Marshal(instanceIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req := new(ecs.DescribeInstancesRequest)
	req.SetRegionId(c.Region)
	req.SetInstanceIds(string(idsJSON))
	resp, err := c.ECS.DescribeInstances(req)
	if err != nil {
		return nil, err
	}
	return parseInstanceList(resp), nil
}

// ListAllInstances returns all instances in the region with their tags.
func (c *Client) ListAllInstances() ([]InstanceInfo, error) {
	req := new(ecs.DescribeInstancesRequest)
	req.SetRegionId(c.Region)
	req.SetPageSize(int32(50))
	resp, err := c.ECS.DescribeInstances(req)
	if err != nil {
		return nil, err
	}
	return parseInstanceList(resp), nil
}

func parseInstanceList(resp *ecs.DescribeInstancesResponse) []InstanceInfo {
	var result []InstanceInfo
	for _, inst := range resp.Body.Instances.Instance {
		info := InstanceInfo{
			ID:              safeStr(inst.InstanceId),
			Name:            safeStr(inst.InstanceName),
			Status:          safeStr(inst.Status),
			VSwitchID:       safeStr(inst.VpcAttributes.VSwitchId),
			SecurityGroupID: safeStr(inst.SecurityGroupIds.SecurityGroupId[0]),
		}
		if len(inst.NetworkInterfaces.NetworkInterface) > 0 {
			ni := inst.NetworkInterfaces.NetworkInterface[0]
			if len(ni.PrivateIpSets.PrivateIpSet) > 0 {
				info.PrivateIP = safeStr(ni.PrivateIpSets.PrivateIpSet[0].PrivateIpAddress)
			}
		}
		pubIPs := inst.PublicIpAddress.IpAddress
		if len(pubIPs) > 0 {
			info.PublicIP = *pubIPs[0]
		}
		info.Tags = make(map[string]string)
		for _, t := range inst.Tags.Tag {
			info.Tags[safeStr(t.TagKey)] = safeStr(t.TagValue)
		}
		result = append(result, info)
	}
	return result
}

// ListInstancesByTag returns instance IDs that match a specific tag key/value.
func (c *Client) ListInstancesByTag(tagKey, tagValue string) ([]string, error) {
	req := new(ecs.DescribeInstancesRequest)
	req.SetRegionId(c.Region)
	req.SetTag([]*ecs.DescribeInstancesRequestTag{
		{Key: &tagKey, Value: &tagValue},
	})

	resp, err := c.ECS.DescribeInstances(req)
	if err != nil {
		return nil, fmt.Errorf("list instances by tag: %w", err)
	}
	var ids []string
	for _, inst := range resp.Body.Instances.Instance {
		ids = append(ids, *inst.InstanceId)
	}
	return ids, nil
}

// FindImageByName returns the image ID for the given exact name, or "" if not found.
func (c *Client) FindImageByName(name string) (string, error) {
	req := new(ecs.DescribeImagesRequest)
	req.SetRegionId(c.Region)
	req.SetImageName(name)
	req.SetStatus("Available")
	resp, err := c.ECS.DescribeImages(req)
	if err != nil {
		return "", err
	}
	if len(resp.Body.Images.Image) == 0 {
		return "", nil
	}
	return *resp.Body.Images.Image[0].ImageId, nil
}

// DeleteImage deletes a custom image by ID.
func (c *Client) DeleteImage(imgID string) error {
	req := new(ecs.DeleteImageRequest)
	req.SetRegionId(c.Region)
	req.SetImageId(imgID)
	_, err := c.ECS.DeleteImage(req)
	return err
}

// CreateImage creates a custom ECS image from a running or stopped instance.
// The image is created asynchronously; poll with DescribeImages until status is "Available".
func (c *Client) CreateImage(instanceID, imageName string) (string, error) {
	req := new(ecs.CreateImageRequest)
	req.SetRegionId(c.Region)
	req.SetInstanceId(instanceID)
	req.SetImageName(imageName)
	req.SetDescription("Built by remote-kind")

	resp, err := c.ECS.CreateImage(req)
	if err != nil {
		return "", fmt.Errorf("create image: %w", err)
	}
	return *resp.Body.ImageId, nil
}

// WaitForImage polls until the custom image is "Available".
func (c *Client) WaitForImage(ctx context.Context, imageID string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
		req := new(ecs.DescribeImagesRequest)
		req.SetRegionId(c.Region)
		req.SetImageId(imageID)

		resp, err := c.ECS.DescribeImages(req)
		if err != nil {
			continue
		}
		for _, img := range resp.Body.Images.Image {
			if *img.ImageId == imageID && *img.Status == "Available" {
				return nil
			}
		}
	}
}

// GetInstancePublicIP returns the first public IP of an instance.
func (c *Client) GetInstancePublicIP(instanceID string) (string, error) {
	idsJSON, err := json.Marshal([]string{instanceID})
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	req := new(ecs.DescribeInstancesRequest)
	req.SetRegionId(c.Region)
	req.SetInstanceIds(string(idsJSON))
	resp, err := c.ECS.DescribeInstances(req)
	if err != nil {
		return "", err
	}
	if len(resp.Body.Instances.Instance) == 0 {
		return "", fmt.Errorf("instance %s not found", instanceID)
	}
	pubIPs := resp.Body.Instances.Instance[0].PublicIpAddress.IpAddress
	if len(pubIPs) == 0 {
		return "", nil
	}
	return *pubIPs[0], nil
}

// ListSGsByVPC returns security group IDs in a VPC.
func (c *Client) ListSGsByVPC(vpcID string) ([]string, error) {
	req := new(ecs.DescribeSecurityGroupsRequest)
	req.SetRegionId(c.Region)
	req.SetVpcId(vpcID)
	resp, err := c.ECS.DescribeSecurityGroups(req)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, sg := range resp.Body.SecurityGroups.SecurityGroup {
		ids = append(ids, *sg.SecurityGroupId)
	}
	return ids, nil
}

// StopInstance stops a running ECS instance.
func (c *Client) StopInstance(instanceID string) error {
	req := new(ecs.StopInstanceRequest)
	req.SetInstanceId(instanceID)
	_, err := c.ECS.StopInstance(req)
	if err != nil {
		return fmt.Errorf("stop instance %s: %w", instanceID, err)
	}
	return nil
}

// WaitUntilStopped polls until the instance reaches Stopped state.
func (c *Client) WaitUntilStopped(ctx context.Context, instanceID string) error {
	idsJSON, err := json.Marshal([]string{instanceID})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req := new(ecs.DescribeInstancesRequest)
	req.SetRegionId(c.Region)
	req.SetInstanceIds(string(idsJSON))
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
		resp, err := c.ECS.DescribeInstances(req)
		if err != nil {
			continue
		}
		for _, inst := range resp.Body.Instances.Instance {
			if inst.Status != nil && *inst.Status == "Stopped" {
				return nil
			}
		}
	}
}

// WaitUntilTerminated polls until all given instances are fully deleted.
func (c *Client) WaitUntilTerminated(ctx context.Context, instanceIDs []string) error {
	idsJSON, err := json.Marshal(instanceIDs)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req := new(ecs.DescribeInstancesRequest)
	req.SetRegionId(c.Region)
	req.SetInstanceIds(string(idsJSON))

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
		resp, err := c.ECS.DescribeInstances(req)
		if err != nil {
			continue
		}
		if len(resp.Body.Instances.Instance) == 0 {
			return nil
		}
	}
}

// DeleteInstances deletes ECS instances by ID. Returns once the API call succeeds
// (the actual deletion happens asynchronously).
func (c *Client) DeleteInstances(instanceIDs []string) error {
	req := new(ecs.DeleteInstancesRequest)
	req.SetRegionId(c.Region)
	req.SetInstanceId(tea.StringSlice(instanceIDs))
	req.SetForce(true)
	_, err := c.ECS.DeleteInstances(req)
	if err != nil {
		return fmt.Errorf("delete instances: %w", err)
	}
	return nil
}
