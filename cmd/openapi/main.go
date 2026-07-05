// Command dechat-openapi dumps the OpenAPI document to openapi.json +
// openapi.yaml in the current directory. Use it to ship a checked-in copy of
// the spec alongside the repo, or to feed code generators without running the
// server:
//
//	go run ./cmd/openapi
//	make openapi
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/pavelananev/dechat/internal/httpapi"
)

func main() {
	doc, err := httpapi.BuildSpec()
	if err != nil {
		log.Fatalf("build spec: %v", err)
	}

	jb, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		log.Fatalf("marshal json: %v", err)
	}
	if err := os.WriteFile("openapi.json", jb, 0o644); err != nil {
		log.Fatalf("write openapi.json: %v", err)
	}

	yb, err := yaml.Marshal(doc)
	if err != nil {
		log.Fatalf("marshal yaml: %v", err)
	}
	if err := os.WriteFile("openapi.yaml", yb, 0o644); err != nil {
		log.Fatalf("write openapi.yaml: %v", err)
	}

	fmt.Println("wrote openapi.json and openapi.yaml")
}
