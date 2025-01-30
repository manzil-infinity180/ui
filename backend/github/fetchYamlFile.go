package FetchYaml

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"gopkg.in/yaml.v2"
)

type Deployment struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name   string            `yaml:"name"`
		Labels map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		Replicas int `yaml:"replicas"`
		Selector struct {
			MatchLabels map[string]string `yaml:"matchLabels"`
		} `yaml:"selector"`
		Template struct {
			Metadata struct {
				Labels map[string]string `yaml:"labels"`
			} `yaml:"metadata"`
			Spec struct {
				Containers []struct {
					Name  string `yaml:"name"`
					Image string `yaml:"image"`
					Ports []struct {
						ContainerPort int `yaml:"containerPort"`
					} `yaml:"ports"`
				} `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

type GitHubFile struct {
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
}
type RepoInfo struct {
	Owner string
	Repo  string
}

func ParseGitHubURL(url string) (RepoInfo, error) {
	url = strings.TrimRight(url, "/")

	url = strings.TrimPrefix(url, "https://github.com/")
	url = strings.TrimPrefix(url, "http://github.com/")
	parts := strings.Split(url, "/")
	if len(parts) != 2 {
		return RepoInfo{}, fmt.Errorf("invalid GitHub URL format. Expected format: https://github.com/owner/repo")
	}
	return RepoInfo{
		Owner: parts[0],
		Repo:  parts[1],
	}, nil
}
func FetchYamlFile(ctx context.Context, github_url string, path string) (*Deployment, error) {
	repoInfo, err := ParseGitHubURL(github_url)
	if err != nil {
		return nil, fmt.Errorf("invalid GitHub URL format: %v", err)
	}
	OWNER := repoInfo.Owner
	REPO := repoInfo.Repo
	github_url = fmt.Sprintf("https://api.github.com/repos/%v/%v/contents/%v", OWNER, REPO, path)
	req, err := http.NewRequestWithContext(ctx, "GET", github_url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed during NewRequestWithContext: %v", err)
	}
	req.Header.Add("Accept", "application/vnd.github.v3+json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed during default client request: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("API request failed with status %d: %s", res.StatusCode, string(body))

	}
	defer res.Body.Close()
	var files []GitHubFile
	if err := json.NewDecoder(res.Body).Decode(&files); err != nil {
		return nil, fmt.Errorf("error decoding response: %v", err)
	}

	var deployment Deployment
	for _, file := range files {
		if strings.HasSuffix(file.Name, ".yml") || strings.HasSuffix(file.Name, ".yaml") {
			content, err := GetYamlFile(ctx, file.DownloadURL)
			if err != nil {
				fmt.Printf("Error downloading %s: %v\n", file.Name, err)
				continue
			}

			fmt.Printf("Downloaded: %s\n", file.Name)

			if err := yaml.Unmarshal([]byte(content), &deployment); err != nil {
				return nil, fmt.Errorf("error Unmarshal response: %v", err)
			}
		}
	}
	return &deployment, nil

}

func GetYamlFile(ctx context.Context, downloadUrl string) (string, error) {

	req, err := http.NewRequestWithContext(ctx, "GET", downloadUrl, nil)
	if err != nil {
		return "", fmt.Errorf("error creating request: %v", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("error downloading file: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status %d", res.StatusCode)
	}
	content, err := io.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response: %v", err)
	}
	return string(content), nil

}
