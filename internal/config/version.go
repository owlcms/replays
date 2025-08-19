package config

var programVersion = "1.8.0"

// GetProgramVersion returns the current version of the program
func GetProgramVersion() string {
	if programVersion == "_" + "TAG_" {
		return "dev"
	}
	return programVersion
}
