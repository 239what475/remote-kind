// Package cluster defines the core data model and orchestration logic
// for remote-kind Kubernetes clusters on Alibaba Cloud.
package cluster

// ── User-facing configuration (from remote-kind.yaml) ──

// ClusterConfig is the top-level structure of remote-kind.yaml.
type ClusterConfig struct {
	Kind       string       `yaml:"kind"`       // "Cluster"
	APIVersion string       `yaml:"apiVersion"` // "remote-kind.x-k8s.io/v1alpha1"
	Name       string       `yaml:"name"`
	Spec       Spec         `yaml:"spec"`
	Build      *BuildConfig `yaml:"build,omitempty"` // build-image settings, not used at runtime
}

// BuildConfig holds settings for the build-image command.
type BuildConfig struct {
	Mirrors map[string]string `yaml:"mirrors,omitempty"` // registry → mirror URL
}

// Spec holds user-specified cluster parameters.
type Spec struct {
	Region       string           `yaml:"region"`           // cn-hangzhou, cn-beijing, ...
	Zone         string           `yaml:"zone"`             // cn-hangzhou-i, ...
	SSHKey       string           `yaml:"sshKey,omitempty"` // path to SSH public key (~/.ssh/id_ed25519.pub)
	ControlPlane ControlPlaneSpec `yaml:"controlPlane"`
	Workers      []WorkerSpec     `yaml:"workers,omitempty"`
	Networking   NetworkingSpec   `yaml:"networking,omitempty"`
	CNI          string           `yaml:"cni,omitempty"` // "flannel"(default) | "none"
}

// ControlPlaneSpec configures the control plane node.
type ControlPlaneSpec struct {
	InstanceType string `yaml:"instanceType"` // default: ecs.e-c1m2.large
}

// WorkerSpec configures a worker node group.
type WorkerSpec struct {
	Name         string `yaml:"name,omitempty"`
	Replicas     int    `yaml:"replicas"`
	InstanceType string `yaml:"instanceType"`
}

// NetworkingSpec configures network CIDRs. All fields have sensible defaults.
type NetworkingSpec struct {
	PodSubnet     string `yaml:"podSubnet,omitempty"`     // default: 10.244.0.0/16
	ServiceSubnet string `yaml:"serviceSubnet,omitempty"` // default: 10.96.0.0/12
}

// ── Runtime state (in-memory only, never persisted) ──

// ClusterState tracks all cloud resources created for a cluster.
// It exists only in memory during create/delete operations.
type ClusterState struct {
	Name   string
	Config *ClusterConfig

	// Cloud resource IDs
	VPCID           string
	VSwitchID       string
	SecurityGroupID string

	// Node IDs
	ControlPlaneID    string
	ControlPlaneIP    string // private IP inside VPC
	ControlPlanePubIP string // public IP for kubeconfig
	WorkerIDs         []string

	// kubeadm credentials (generated during init, used for join)
	JoinCommand string // full "kubeadm join ..." command
	Kubeconfig  string // admin.conf content
}

// ── Tag constants (label-driven resource discovery) ──

const (
	LabelCluster = "remote-kind-cluster" // value: <name>
	LabelRole    = "remote-kind-role"    // value: control-plane | worker
)

const (
	RoleControlPlane = "control-plane"
	RoleWorker       = "worker"
)

// ── Defaults ──

const (
	DefaultRegion         = "cn-beijing"
	DefaultZone           = "cn-beijing-h"
	DefaultInstanceType   = "ecs.e-c1m2.large"
	DefaultSystemDiskSize = 40
	DefaultReplicas       = 1
	DefaultCNI            = "flannel"
	DefaultVpcCIDR        = "10.0.0.0/16"
	DefaultVSwitchCIDR    = "10.0.1.0/24"
	DefaultPodSubnet      = "10.244.0.0/16"
	DefaultServiceSubnet  = "10.96.0.0/12"
	DefaultAPIServerPort  = 6443
)

// ApplyDefaults fills zero-value fields with sensible defaults.
func (c *ClusterConfig) ApplyDefaults() {
	if c.Spec.Region == "" {
		c.Spec.Region = DefaultRegion
	}
	if c.Spec.Zone == "" {
		c.Spec.Zone = DefaultZone
	}
	if c.Spec.ControlPlane.InstanceType == "" {
		c.Spec.ControlPlane.InstanceType = DefaultInstanceType
	}
	if c.Spec.CNI == "" {
		c.Spec.CNI = DefaultCNI
	}
	if c.Spec.Networking.PodSubnet == "" {
		c.Spec.Networking.PodSubnet = DefaultPodSubnet
	}
	if c.Spec.Networking.ServiceSubnet == "" {
		c.Spec.Networking.ServiceSubnet = DefaultServiceSubnet
	}

	if len(c.Spec.Workers) == 0 {
		c.Spec.Workers = []WorkerSpec{
			{
				Name:     "default",
				Replicas: DefaultReplicas,
			},
		}
	}
	for i := range c.Spec.Workers {
		if c.Spec.Workers[i].InstanceType == "" {
			c.Spec.Workers[i].InstanceType = DefaultInstanceType
		}
		if c.Spec.Workers[i].Replicas == 0 {
			c.Spec.Workers[i].Replicas = DefaultReplicas
		}
	}
}

// TotalNodes returns the total number of nodes (control plane + workers).
func (c *ClusterConfig) TotalNodes() int {
	total := 1 // control plane
	for _, w := range c.Spec.Workers {
		total += w.Replicas
	}
	return total
}
