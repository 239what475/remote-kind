// Package bootstrap generates cloud-init configurations and shell scripts
// for bootstrapping Kubernetes nodes via Cloud Assistant.
package bootstrap

import (
	"bytes"
	"fmt"
	"text/template"
)

type CloudInitData struct {
	Hostname    string
	K8sVersion  string
	JoinCommand string
	SSHKey      string // public key content to add to authorized_keys
}

const cloudInitTemplate = `#cloud-config
hostname: {{.Hostname}}
{{- if .SSHKey}}
ssh_authorized_keys:
  - {{.SSHKey}}
disable_root: false
ssh_pwauth: false
{{- end}}
runcmd:
  - echo ok > /tmp/cloud-init-ok
{{- if .JoinCommand}}
  - sh -c '{{.JoinCommand}}' 2>&1 | tee /tmp/kubeadm-join.log
{{- end}}
`

// Note: build-image does NOT use this — it passes its own minimal UserData.

var tmpl = template.Must(template.New("cloudinit").Parse(cloudInitTemplate))

func GenerateCloudInit(data *CloudInitData) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render cloud-init: %w", err)
	}
	return buf.String(), nil
}
