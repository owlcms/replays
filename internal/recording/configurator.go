//go:build windows || linux

package recording

import "github.com/owlcms/replays/internal/config"

// SetCameraConfigs sets the available camera configurations.
func SetCameraConfigs(configs []config.CameraConfiguration) {
	CameraConfigs = configs
}
