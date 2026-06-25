package cluster

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
