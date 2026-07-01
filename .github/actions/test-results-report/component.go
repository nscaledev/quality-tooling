package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ComponentMetadata struct {
	Name    string
	Version string
	Ref     string
	Repo    string
}

func (component ComponentMetadata) IsZero() bool {
	return component.Name == "" && component.Version == "" && component.Ref == "" && component.Repo == ""
}

func componentMetadataFromConfig(config Config) ComponentMetadata {
	return ComponentMetadata{
		Name:    config.ComponentName,
		Version: config.ComponentVersion,
		Ref:     config.ComponentRef,
		Repo:    config.ComponentRepo,
	}
}

func resolveComponentMetadata(ctx context.Context, config Config) (Config, error) {
	if strings.TrimSpace(config.ComponentVersion) != "" || strings.TrimSpace(config.ComponentVersionURL) == "" {
		return config, nil
	}

	version, err := fetchComponentVersion(ctx, config.ComponentVersionURL, config.ComponentVersionToken, config.ComponentVersionTimeout)
	if err != nil {
		return config, err
	}
	config.ComponentVersion = version
	return config, nil
}

func fetchComponentVersion(ctx context.Context, rawURL, token string, timeout time.Duration) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("component version URL is empty")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("component-version-url must be an absolute URL")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("build component version request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if token = strings.TrimSpace(token); token != "" {
		if !strings.HasPrefix(strings.ToLower(token), "bearer ") {
			token = "Bearer " + token
		}
		request.Header.Set("Authorization", token)
	}

	client := &http.Client{Timeout: timeout}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("GET component version: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil {
		return "", fmt.Errorf("read component version response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("component version endpoint returned %d: %s", response.StatusCode, truncate(cleanOneLine(string(body)), 300))
	}

	version, err := componentVersionFromJSON(body)
	if err != nil {
		return "", err
	}
	return version, nil
}

func componentVersionFromJSON(data []byte) (string, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return "", fmt.Errorf("parse component version JSON: %w", err)
	}
	version := findComponentVersion(value)
	if version == "" {
		return "", fmt.Errorf("component version response did not contain a version field")
	}
	return version, nil
}

func findComponentVersion(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		for _, item := range typed {
			if version := findComponentVersion(item); version != "" {
				return version
			}
		}
	case map[string]any:
		for _, key := range []string{"version", "serviceVersion", "componentVersion", "gitVersion", "tag"} {
			if version := findComponentVersion(typed[key]); version != "" {
				return version
			}
		}
		for _, key := range []string{"data", "metadata", "status"} {
			if version := findComponentVersionContainer(typed[key]); version != "" {
				return version
			}
		}
	}
	return ""
}

func findComponentVersionContainer(value any) string {
	switch value.(type) {
	case map[string]any, []any:
		return findComponentVersion(value)
	default:
		return ""
	}
}
