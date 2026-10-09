package conf

import (
	"os"
)

func IsDebug() bool {
	return os.Getenv(ENV_PREFIX+"_DEBUG") == "true"
}
