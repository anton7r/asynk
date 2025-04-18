package newestfile

import (
	"asynk/files"
	"regexp"
	"strings"
)

var newestFilePattern = regexp.MustCompile(`~{([^}]+)}`)

func Interpolate(input string) string {
	return newestFilePattern.ReplaceAllStringFunc(input, func(match string) string {
		key := strings.Trim(match[2:len(match)-1], " ")

		inDir, err := files.GetFilesInDir(key)
		if err != nil {
			return match // Return the original pattern if the directory is not found
		}

		//log.Println("Newest file in directory: ", inDir)

		if len(inDir) == 0 {
			return match // Return the original pattern if no files are found
		}

		newestFile := files.GetNewestFile(inDir)
		if newestFile != "" {
			return newestFile // Return the path of the newest file
		}

		return match // Return the original pattern if the env variable is not found
	})
}
