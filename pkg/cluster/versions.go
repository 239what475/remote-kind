package cluster

// Version is the source of truth — update on each release.
// GitCommit, BuildTime, and Release are injected via ldflags at build time:
//
//	go build -ldflags "-X ...GitCommit=$(git rev-parse --short HEAD)
//	                   -X ...BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)
//	                   -X ...Release=true"
var (
	Version   = "v0.1.1"
	GitCommit = "unknown"
	BuildTime = "unknown"
	Release   = "false" // "true" = clean user-facing output
)

// Centralized version/image constants. To upgrade the cluster, change these
// values, rebuild the image, and create a new cluster.
const (
	K8sVersion        = "v1.36.2"
	ImageName         = "remote-kind-v1.36"
	KubeadmAPIVersion = "kubeadm.k8s.io/v1beta4"

	// Flannel (ghcr.io/flannel-io/*)
	FlannelImage    = "ghcr.io/flannel-io/flannel:v0.28.5"
	FlannelCNIImage = "ghcr.io/flannel-io/flannel-cni-plugin:v1.9.1-flannel1"
)
