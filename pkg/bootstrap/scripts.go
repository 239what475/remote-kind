package bootstrap

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

//go:embed templates/*
var scriptFS embed.FS

var (
	installTmpl = template.Must(template.ParseFS(scriptFS, "templates/install.sh"))
	initTmpl    = template.Must(template.ParseFS(scriptFS, "templates/kubeadm-init.sh"))
	joinTmpl    = template.Must(template.ParseFS(scriptFS, "templates/kubeadm-join.sh"))
)

// InstallScriptData holds parameters for the build-image install script.
type InstallScriptData struct {
	Mirrors         map[string]string // registry → mirror URL
	K8sMinor        string            // e.g. "v1.36"
	K8sVersion      string            // e.g. "v1.36.2"
	FlannelImage    string
	FlannelCNIImage string
}

// RenderInstallScript generates the full image build install script.
func RenderInstallScript(data *InstallScriptData) (string, error) {
	var buf bytes.Buffer
	if err := installTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render install script: %w", err)
	}
	return buf.String(), nil
}

// InitScriptData holds parameters for the kubeadm init script.
type InitScriptData struct {
	KubeadmConfig string // full kubeadm ClusterConfiguration YAML
}

// RenderInitScript generates the kubeadm init shell script.
func RenderInitScript(data *InitScriptData) (string, error) {
	var buf bytes.Buffer
	if err := initTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render init script: %w", err)
	}
	return buf.String(), nil
}

// JoinScriptData holds parameters for the kubeadm join script.
type JoinScriptData struct {
	JoinCommand string // full "kubeadm join ..." command
}

// RenderJoinScript generates the kubeadm join shell script.
func RenderJoinScript(data *JoinScriptData) (string, error) {
	var buf bytes.Buffer
	if err := joinTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render join script: %w", err)
	}
	return buf.String(), nil
}
