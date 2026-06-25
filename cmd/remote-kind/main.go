package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/what/remote-kind/pkg/aliyun"
	"github.com/what/remote-kind/pkg/cluster"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog/v2"
)

var configPath string
var clusterName string
var retain, wait bool
var targetWorkers int

func main() {
	klog.InitFlags(nil)

	root := &cobra.Command{
		Use:           "remote-kind",
		Short:         "Kubernetes clusters on Alibaba Cloud",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddGroup(&cobra.Group{ID: "cluster", Title: "Cluster Management:"})
	root.AddGroup(&cobra.Group{ID: "ops", Title: "Operations:"})

	getCmd := &cobra.Command{Use: "get", Short: "Get clusters, nodes, or kubeconfig"}
	getCmd.AddCommand(getClustersCmd(), getNodesCmd(), getKubeconfigCmd())

	createCmd := createCmd()
	deleteCmd := deleteCmd()
	scaleCmd := scaleCmd()
	showCmd := showCmd()
	buildImageCmd := buildImageCmd()
	sshCmd := sshCmd()

	createCmd.GroupID = "cluster"
	deleteCmd.GroupID = "cluster"
	scaleCmd.GroupID = "cluster"
	showCmd.GroupID = "cluster"
	buildImageCmd.GroupID = "ops"
	sshCmd.GroupID = "ops"
	getCmd.GroupID = "ops"

	initCmd := initCmd()
	versionCmd := versionCmd()
	completionCmd := completionCmd()
	initCmd.GroupID = "ops"
	versionCmd.GroupID = "ops"
	completionCmd.GroupID = "ops"

	root.AddCommand(initCmd, createCmd, deleteCmd, showCmd, scaleCmd, getCmd, buildImageCmd, sshCmd, versionCmd, completionCmd)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Generate a default remote-kind.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cluster.WriteDefaultConfig("remote-kind.yaml")
		},
	}
}

func createCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create cluster",
		Short: "Create a Kubernetes cluster on Alibaba Cloud",
		RunE: func(cmd *cobra.Command, args []string) error {
			start := time.Now()
			cfg, err := cluster.LoadConfig(configPath)
			if err != nil {
				return err
			}
			client, err := aliyun.NewClient(cfg.Spec.Region)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			state := &cluster.ClusterState{Name: cfg.Name, Config: cfg}
			if err := state.Create(ctx, client); err != nil {
				if !retain {
					if err := cluster.DeleteByName(context.Background(), client, cfg.Name); err != nil {
						klog.Warningf("rollback delete: %v", err)
					}
				}
				return fmt.Errorf("create cluster: %w", err)
			}

			// Merge kubeconfig
			kcPath := os.ExpandEnv("$HOME/.kube/config")
			if err := os.MkdirAll(os.ExpandEnv("$HOME/.kube"), 0700); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "kubeconfig dir: %v\n", err)
			}
			if err := mergeKubeconfig(kcPath, cfg.Name, []byte(state.Kubeconfig), state.ControlPlanePubIP); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "kubeconfig merge failed: %v\n", err)
			} else {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "kubeconfig merged (context: kind-%s)\n", cfg.Name)
			}

			// Install CNI
			switch cfg.Spec.CNI {
			case "flannel":
				yamlData, err := cluster.FlannelYAML()
				if err != nil {
					return fmt.Errorf("read flannel yaml: %w", err)
				}
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Installing Flannel CNI...")
				cniCmd := exec.Command("kubectl", "apply", "-f", "-", "--validate=false")
				cniCmd.Stdin = bytes.NewReader(yamlData)
				cniCmd.Stdout = cmd.OutOrStdout()
				cniCmd.Stderr = cmd.ErrOrStderr()
				if err := cniCmd.Run(); err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "CNI install failed: %v\n", err)
				}
			}

			if wait {
				_, _ = fmt.Fprint(cmd.ErrOrStderr(), "Waiting for nodes to be Ready...")
				waitCmd := exec.Command("kubectl", "wait", "--for=condition=Ready", "nodes", "--all", "--timeout=5m")
				waitCmd.Stdout = cmd.OutOrStdout()
				waitCmd.Stderr = cmd.ErrOrStderr()
				if err := waitCmd.Run(); err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "\nWarning: some nodes not ready: %v\n", err)
				}
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nCluster %q ready in %s\n", cfg.Name, time.Since(start).Round(time.Second))
			if state.ControlPlanePubIP != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  ssh root@%s\n", state.ControlPlanePubIP)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  kubectl --context kind-%s get nodes\n", cfg.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "remote-kind.yaml", "Path to config file")
	cmd.Flags().BoolVar(&retain, "retain", false, "Keep instances on failure")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for all nodes to be Ready")
	return cmd
}

func deleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete cluster",
		Short: "Delete a cluster and all its cloud resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			name, region, err := resolveCluster(clusterName, configPath)
			if err != nil {
				return err
			}

			client, err := aliyun.NewClient(region)
			if err != nil {
				return err
			}
			if err := cluster.DeleteByName(cmd.Context(), client, name); err != nil {
				return err
			}

			kcPath := os.ExpandEnv("$HOME/.kube/config")
			removeKubeconfigEntry(kcPath, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "remote-kind.yaml", "Path to config file (optional if --name is set)")
	cmd.Flags().StringVar(&clusterName, "name", "", "Cluster name (bypasses config file)")
	return cmd
}

func scaleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scale",
		Short: "Scale worker nodes up or down",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cluster.LoadConfig(configPath)
			if err != nil {
				return err
			}
			client, err := aliyun.NewClient(cfg.Spec.Region)
			if err != nil {
				return err
			}
			return cluster.ScaleWorkers(cmd.Context(), client, cfg, targetWorkers)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "remote-kind.yaml", "Path to config file")
	cmd.Flags().IntVar(&targetWorkers, "workers", 0, "Target number of worker nodes")
	_ = cmd.MarkFlagRequired("workers")
	return cmd
}

func buildImageCmd() *cobra.Command {
	var force, testMode bool
	cmd := &cobra.Command{
		Use:   "build-image",
		Short: "Build a custom ECS image with pre-installed Kubernetes",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cluster.LoadConfig(configPath)
			if err != nil {
				return err
			}
			client, err := aliyun.NewClient(cfg.Spec.Region)
			if err != nil {
				return err
			}
			return cluster.BuildImage(cmd.Context(), client, cfg, force, testMode)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "remote-kind.yaml", "Path to config file")
	cmd.Flags().BoolVar(&force, "force", false, "Rebuild even if image exists")
	cmd.Flags().BoolVar(&testMode, "test", false, "Run kubeadm init test, skip image creation")
	return cmd
}

func showCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show cluster",
		Short: "Show cluster details from cloud resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			name, region, err := resolveCluster(clusterName, configPath)
			if err != nil {
				return err
			}

			client, err := aliyun.NewClient(region)
			if err != nil {
				return err
			}
			return cluster.ShowCluster(cmd.Context(), client, name)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "remote-kind.yaml", "Path to config file (optional if --name is set)")
	cmd.Flags().StringVar(&clusterName, "name", "", "Cluster name (bypasses config file)")
	return cmd
}

func getClustersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clusters",
		Short: "List all remote-kind clusters",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := aliyun.NewClient("cn-beijing")
			if err != nil {
				return err
			}
			clusters, err := cluster.ListClusters(cmd.Context(), c)
			if err != nil {
				return err
			}
			if len(clusters) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No clusters found")
				return nil
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "%-16s %-12s %-18s %s\n", "NAME", "REGION", "CP_IP", "NODES")
			for _, cl := range clusters {
				_, _ = fmt.Fprintf(out, "%-16s %-12s %-18s %d\n", cl.Name, cl.Region, cl.CPIP, cl.NodeCount)
			}
			return nil
		},
	}
}

func getNodesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nodes",
		Short: "List nodes in a cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := aliyun.NewClient("cn-beijing")
			if err != nil {
				return err
			}
			nodes, err := cluster.ListNodes(cmd.Context(), c, clusterName)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "%-24s %-16s %-14s %-18s %s\n", "NAME", "ROLE", "PRIVATE_IP", "PUBLIC_IP", "STATUS")
			for _, n := range nodes {
				_, _ = fmt.Fprintf(out, "%-24s %-16s %-14s %-18s %s\n", n.Name, n.Role, n.PrivateIP, n.PublicIP, n.Status)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&clusterName, "name", "", "Cluster name")
	return cmd
}

func getKubeconfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kubeconfig",
		Short: "Print kubeconfig for a cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := aliyun.NewClient("cn-beijing")
			if err != nil {
				return err
			}
			insts, err := c.ListAllInstances()
			if err != nil {
				return fmt.Errorf("list instances: %w", err)
			}
			var cpID string
			for _, i := range insts {
				if i.Tags[aliyun.TagCluster] == clusterName && i.Tags[aliyun.TagRole] == aliyun.RoleControlPlane {
					cpID = i.ID
					break
				}
			}
			if cpID == "" {
				return fmt.Errorf("no control plane for cluster %q", clusterName)
			}
			kc, err := c.RunCommand(cmd.Context(), cpID, "cat /etc/kubernetes/admin.conf", "get-kc")
			if err != nil {
				return err
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), kc)
			return nil
		},
	}
	cmd.Flags().StringVar(&clusterName, "name", "", "Cluster name")
	return cmd
}

func completionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion script",
		Long: `To load completion:

  bash:  source <(remote-kind completion bash)
  zsh:   source <(remote-kind completion zsh)
  fish:  remote-kind completion fish | source`,
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			}
			return nil
		},
	}
}

func sshCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh <node>",
		Short: "SSH into a cluster node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeName := args[0]
			region := "cn-beijing"
			if clusterName != "" {
				cfg, _ := cluster.LoadConfig(configPath)
				if cfg != nil {
					region = cfg.Spec.Region
				}
			} else {
				cfg, err := cluster.LoadConfig(configPath)
				if err != nil {
					return err
				}
				region = cfg.Spec.Region
			}

			client, err := aliyun.NewClient(region)
			if err != nil {
				return err
			}

			insts, err := client.ListAllInstances()
			if err != nil {
				return fmt.Errorf("list instances: %w", err)
			}
			var pubIP string
			for _, inst := range insts {
				if inst.Name == nodeName {
					pubIP = inst.PublicIP
					break
				}
			}
			if pubIP == "" {
				return fmt.Errorf("node %q not found or has no public IP", nodeName)
			}

			if net.ParseIP(pubIP) == nil {
				return fmt.Errorf("invalid IP: %s", pubIP)
			}
			sshCmd := exec.Command("ssh", "-o", "StrictHostKeyChecking=no", "root@"+pubIP) // IP comes from DescribeInstances, trusted source
			sshCmd.Stdin = os.Stdin
			sshCmd.Stdout = cmd.OutOrStdout()
			sshCmd.Stderr = cmd.ErrOrStderr()
			return sshCmd.Run()
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "remote-kind.yaml", "Path to config file")
	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use: "version", Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
		},
	}
}

// resolveCluster returns the cluster name and region from --name flag or config file.
func resolveCluster(name, configPath string) (string, string, error) {
	if name != "" {
		cfg, _ := cluster.LoadConfig(configPath)
		if cfg != nil {
			return name, cfg.Spec.Region, nil
		}
		return name, "cn-beijing", nil
	}
	cfg, err := cluster.LoadConfig(configPath)
	if err != nil {
		return "", "", fmt.Errorf("no --name given and no config found at %q: %w", configPath, err)
	}
	return cfg.Name, cfg.Spec.Region, nil
}

func removeKubeconfigEntry(path, clusterName string) {
	ctxName := fmt.Sprintf("kind-%s", clusterName)
	existing, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return
	}
	if _, ok := existing.Contexts[ctxName]; !ok {
		return
	}
	delete(existing.Clusters, ctxName)
	delete(existing.AuthInfos, ctxName)
	delete(existing.Contexts, ctxName)
	if existing.CurrentContext == ctxName {
		existing.CurrentContext = ""
	}
	if err := clientcmd.WriteToFile(*existing, path); err != nil {
		klog.Warningf("remove kubeconfig entry: %v", err)
	}
}

func mergeKubeconfig(path, clusterName string, raw []byte, publicIP string) error {
	if publicIP != "" {
		raw = []byte(strings.ReplaceAll(string(raw), "server: https://10.", "server: https://"+publicIP+":6443\n    # was: https://10."))
	}
	newCfg, err := clientcmd.Load(raw)
	if err != nil {
		return os.WriteFile(path, raw, 0600)
	}
	existing, err := clientcmd.LoadFromFile(path)
	if err != nil {
		existing = clientcmdapi.NewConfig()
	}
	var clusterEntry *clientcmdapi.Cluster
	var authInfo *clientcmdapi.AuthInfo
	for _, v := range newCfg.Clusters {
		clusterEntry = v
		break
	}
	for _, v := range newCfg.AuthInfos {
		authInfo = v
		break
	}
	ctxName := fmt.Sprintf("kind-%s", clusterName)
	existing.Clusters[ctxName] = clusterEntry
	existing.AuthInfos[ctxName] = authInfo
	existing.Contexts[ctxName] = clientcmdapi.NewContext()
	existing.Contexts[ctxName].Cluster = ctxName
	existing.Contexts[ctxName].AuthInfo = ctxName
	existing.CurrentContext = ctxName
	return clientcmd.WriteToFile(*existing, path)
}
