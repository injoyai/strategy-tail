package extend

import "os"

// exists reports whether the legacy file exists.
func exists(name string) bool {
	stat, err := os.Stat(name)
	return stat != nil && !os.IsNotExist(err)
}
