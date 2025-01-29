package config

var programVersion = "0.9.4"

// GetProgramVersion returns the current version of the program
func GetProgramVersion() string {
	if programVersion == "_" + "TAG_" {
		return "dev"
	}
	return programVersion
}
