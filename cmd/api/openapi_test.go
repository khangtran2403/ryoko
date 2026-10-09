package main

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"testing"
)

var registeredRoutePattern = regexp.MustCompile(`"(GET|POST|PUT|DELETE) ([^"]+)"`)

func TestOpenAPIDocumentsEveryRegisteredRoute(t *testing.T) {
	registered := make(map[string]struct{})
	for _, sourceFile := range []string{"api.go", "main.go"} {
		source, err := os.ReadFile(sourceFile)
		if err != nil {
			t.Fatalf("read %s: %v", sourceFile, err)
		}
		for _, match := range registeredRoutePattern.FindAllStringSubmatch(string(source), -1) {
			registered[match[1]+" "+match[2]] = struct{}{}
		}
	}

	specFile, err := os.Open("../../openapi.yaml")
	if err != nil {
		t.Fatalf("open openapi.yaml: %v", err)
	}
	defer specFile.Close()

	documented := make(map[string]struct{})
	currentPath := ""
	scanner := bufio.NewScanner(specFile)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "  /") && strings.HasSuffix(line, ":") {
			currentPath = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		if currentPath == "" || !strings.HasPrefix(line, "    ") {
			continue
		}
		method := strings.TrimSuffix(strings.TrimSpace(line), ":")
		switch method {
		case "get", "post", "put", "delete", "patch":
			documented[strings.ToUpper(method)+" "+currentPath] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan openapi.yaml: %v", err)
	}

	for route := range registered {
		if _, ok := documented[route]; !ok {
			t.Errorf("registered route is missing from OpenAPI: %s", route)
		}
	}
	for route := range documented {
		if _, ok := registered[route]; !ok {
			t.Errorf("OpenAPI documents an unregistered route: %s", route)
		}
	}
}

func TestOpenAPIOperationIDsAreUnique(t *testing.T) {
	contents, err := os.ReadFile("../../openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	seen := make(map[string]struct{})
	for _, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "operationId:") {
			continue
		}
		operationID := strings.TrimSpace(strings.TrimPrefix(line, "operationId:"))
		if operationID == "" {
			t.Error("OpenAPI contains an empty operationId")
			continue
		}
		if _, exists := seen[operationID]; exists {
			t.Errorf("duplicate OpenAPI operationId: %s", operationID)
		}
		seen[operationID] = struct{}{}
	}
	if len(seen) != 53 {
		t.Errorf("operationId count = %d, want 53", len(seen))
	}
}
