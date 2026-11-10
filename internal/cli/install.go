package cli

import "github.com/boarding-party/boardingparty/pkg/install"

// Names and helpers from pkg/install, under the names the CLI has always used.
const (
	directorDeploy  = install.DirectorDeployment
	adminSecret     = install.AdminSecret
	defaultImageTag = install.DefaultImageTag
)

var (
	forward        = install.Forward
	readAdminToken = install.ReadAdminToken
)
