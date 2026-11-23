package config

import (
	"bufio"
	"os"
	"strings"
)

// LoadEnv reads a .env file and populates environment variables if not already set.
func LoadEnv(filenames ...string) error {
	if len(filenames) == 0 {
		filenames = []string{".env"}
	}

	for _, filename := range filenames {
		file, err := os.Open(filename)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if len(line) == 0 || strings.HasPrefix(line, "#") {
				continue
			}

			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				// Don't overwrite existing environment variables
				if os.Getenv(key) == "" {
					_ = os.Setenv(key, val)
				}
			}
		}

		if err := scanner.Err(); err != nil {
			return err
		}
	}
	return nil
}
