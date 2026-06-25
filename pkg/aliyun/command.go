package aliyun

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	"github.com/alibabacloud-go/tea/tea"
)

// RunCommand executes a shell script on a single ECS instance via Cloud Assistant.
func (c *Client) RunCommand(ctx context.Context, instanceID, script, description string) (string, error) {
	req := new(ecs.RunCommandRequest)
	req.SetRegionId(c.Region)
	req.SetType("RunShellScript")
	req.SetCommandContent(script) // plain text, SDK handles base64 encoding
	req.SetInstanceId(tea.StringSlice([]string{instanceID}))
	req.SetTimeout(int64(300))
	if description != "" {
		req.SetDescription(description)
	}

	resp, err := c.ECS.RunCommand(req)
	if err != nil {
		return "", fmt.Errorf("run command on %s: %w", instanceID, err)
	}
	invokeID := *resp.Body.InvokeId

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}

		results, err := c.pollResults(invokeID)
		if err != nil {
			return "", err
		}
		if results == nil {
			continue // still running
		}
		if out, ok := results[instanceID]; ok {
			return out, nil
		}
		return "", fmt.Errorf("no result for instance %s", instanceID)
	}
}

// RunCommandParallel executes the same script on multiple instances.
func (c *Client) RunCommandParallel(ctx context.Context, instanceIDs []string, script, description string) (map[string]string, error) {
	req := new(ecs.RunCommandRequest)
	req.SetRegionId(c.Region)
	req.SetType("RunShellScript")
	req.SetCommandContent(script) // plain text, SDK handles base64 encoding
	req.SetInstanceId(tea.StringSlice(instanceIDs))
	req.SetTimeout(int64(300))
	if description != "" {
		req.SetDescription(description)
	}

	resp, err := c.ECS.RunCommand(req)
	if err != nil {
		return nil, fmt.Errorf("run command parallel: %w", err)
	}
	invokeID := *resp.Body.InvokeId

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}

		results, err := c.pollResults(invokeID)
		if err != nil {
			return nil, err
		}
		if results != nil {
			return results, nil
		}
	}
}

// pollResults queries invocation results. Returns nil,nil if still running.
func (c *Client) pollResults(invokeID string) (map[string]string, error) {
	query := new(ecs.DescribeInvocationResultsRequest)
	query.SetRegionId(c.Region)
	query.SetInvokeId(invokeID)

	resp, err := c.ECS.DescribeInvocationResults(query)
	if err != nil {
		return nil, err
	}

	inv := resp.Body.Invocation
	if inv == nil || inv.InvocationResults == nil {
		return nil, nil
	}

	results := make(map[string]string)
	allTerminal := true

	for _, r := range inv.InvocationResults.InvocationResult {
		id := safeStr(r.InstanceId)
		status := safeStr(r.InvocationStatus)

		switch status {
		case "Success", "Finished":
			out := safeStr(r.Output)
			decoded, err := base64.StdEncoding.DecodeString(out)
			if err != nil {
				decoded = []byte(out)
			}
			if len(decoded) > 0 {
				results[id] = string(decoded)
			} else {
				results[id] = out
			}
		case "Failed":
			return nil, fmt.Errorf("command failed on %s: %s", id, safeStr(r.ErrorInfo))
		default:
			// "Running", "Pending", "Stopping"
			allTerminal = false
		}
	}

	if allTerminal {
		return results, nil
	}
	return nil, nil
}

// WaitForCloudAssistant polls until Cloud Assistant agent is online.
func (c *Client) WaitForCloudAssistant(ctx context.Context, instanceIDs []string) error {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		req := new(ecs.DescribeCloudAssistantStatusRequest)
		req.SetRegionId(c.Region)
		req.SetInstanceId(tea.StringSlice(instanceIDs))

		resp, err := c.ECS.DescribeCloudAssistantStatus(req)
		if err != nil {
			continue
		}

		allOnline := true
		for _, s := range resp.Body.InstanceCloudAssistantStatusSet.InstanceCloudAssistantStatus {
			if s.CloudAssistantStatus == nil || *s.CloudAssistantStatus != "true" {
				allOnline = false
				break
			}
		}
		if allOnline {
			return nil
		}
	}
}

// WaitForCloudInitTimeout is a convenience wrapper that adds a timeout via context.
func (c *Client) WaitForCloudInitTimeout(ctx context.Context, instanceID string, timeout time.Duration) {
	ciCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c.WaitForCloudInit(ciCtx, instanceID)
}

// WaitForCloudInit polls until the cloud-init completion marker exists.
// It is best-effort — subsequent RunCommand operations do not depend on cloud-init.
// Timeout is controlled by ctx.
func (c *Client) WaitForCloudInit(ctx context.Context, instanceID string) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
		cmdCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		out, err := c.RunCommand(cmdCtx, instanceID, "cat /tmp/cloud-init-ok", "")
		cancel()
		if err == nil && strings.TrimSpace(out) != "" {
			return
		}
	}
}

func safeStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
