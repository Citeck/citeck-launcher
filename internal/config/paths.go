package config

import (
	"os"
	"path/filepath"
)

const (
	defaultHome = "/opt/citeck"
	defaultRun  = "/run/citeck"
	socketFile  = "daemon.sock"
)

func HomeDir() string {
	if v := os.Getenv("CITECK_HOME"); v != "" {
		return v
	}
	return defaultHome
}

func RunDir() string {
	if v := os.Getenv("CITECK_RUN"); v != "" {
		return v
	}
	return defaultRun
}

func ConfDir() string {
	return filepath.Join(HomeDir(), "conf")
}

func DataDir() string {
	return filepath.Join(HomeDir(), "data")
}

func LogDir() string {
	return filepath.Join(HomeDir(), "log")
}

func NamespaceConfigPath() string {
	return filepath.Join(ConfDir(), "namespace.yml")
}

func SocketPath() string {
	return filepath.Join(RunDir(), socketFile)
}

func DaemonLogPath() string {
	return filepath.Join(LogDir(), "daemon.log")
}
