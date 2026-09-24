// Integration subprocess double: converter copies HTTP fixture JSON, while the
// core validates JSON and can deliberately reject a candidate.
package main

import (
	"encoding/json"
	"os"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "check" || args[0] == "-t") {
		path := args[2]
		if args[0] == "-t" && (len(args) != 5 || args[1] != "-f" || args[3] != "-d") {
			os.Exit(1)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			os.Exit(1)
		}
		var config map[string]json.RawMessage
		if json.Unmarshal(data, &config) != nil || config["reject"] != nil {
			os.Exit(1)
		}
		return
	}

	if len(args) > 0 && args[0] == "restart" {
		os.Exit(1)
	}

	if len(args) == 2 && args[0] == "restart-ok" {
		f, err := os.OpenFile(args[1], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(1)
		}
		_, err = f.WriteString("restart\n")
		_ = f.Close()
		if err != nil {
			os.Exit(1)
		}
		return
	}

	if len(args) == 0 {
		os.Exit(1)
	}

	data, err := os.ReadFile(args[0])
	if err != nil {
		os.Exit(1)
	}

	output := ""
	for i := 1; i < len(args)-1; i++ {
		if args[i] == "--out" {
			output = args[i+1]
		}
	}
	if os.WriteFile(output, data, 0600) != nil {
		os.Exit(1)
	}
}
