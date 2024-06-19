package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gosimple/slug"
	"gopkg.in/yaml.v2"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/charmbracelet/log"
	"github.com/go-resty/resty/v2"
	"github.com/slashtechno/cross-blogger/cobra/pkg/oauth"
	"github.com/spf13/afero"
)

type Destination interface {
	Push(PostData, PlatformOptions) error
	GetName() string
	GetType() string
}

type Source interface {
	Pull(PlatformOptions) (PostData, error)
	GetName() string
	GetType() string
}

type PlatformOptions struct {
	AccessToken string
	BlogId      string
	PostUrl     string
}

type PostData struct {
	Title    string
	Html     string
	Markdown string
	// Other fields that are probably needed are canonical URL, publish date, and description
	CanonicalUrl string
}

// type PlatformParent struct {
// 	Name string
// }

// func (p PlatformParent) Push() {
// 	log.Error("child class must implement this method")
// }

type Blogger struct {
	Name    string
	BlogUrl string
}

// Return the access token, refresh token (if one was not provided), and an error (if one occurred).
// The access and refresh tokens are only returned if an error did not occur.
// In Google Cloud, create OAuth client credentials for a desktop app and enable the Blogger API.
func (b Blogger) authorize(clientId string, clientSecret string, providedRefreshToken string) (string, string, error) {
	oauthConfig := oauth.Config{
		ClientID:     clientId,
		ClientSecret: clientSecret,
		Port:         "8080",
	}
	var refreshToken string
	var err error
	if providedRefreshToken != "" {
		log.Info("Using provided refresh token")
		refreshToken = providedRefreshToken
	} else {
		log.Info("No refresh token provided, starting OAuth flow")
		refreshToken, err = oauth.GetGoogleRefreshToken(oauthConfig)
		if err != nil {
			return "", "", err
		}
	}
	accessToken, err := oauth.GetGoogleAccessToken(oauthConfig, refreshToken)
	if err != nil {
		// Not returning the refresh token because it may have been invalid
		return "", "", err
	}
	log.Info("", "access token", accessToken)
	if providedRefreshToken != "" {
		return accessToken, providedRefreshToken, nil
	}
	return accessToken, refreshToken, nil

}
func (b Blogger) getBlogId(accessToken string) (string, error) {
	client := resty.New()
	resp, err := client.R().SetHeader("Authorization", fmt.Sprintf("Bearer %s", accessToken)).SetResult(&map[string]interface{}{}).Get("https://www.googleapis.com/blogger/v3/blogs/byurl?url=" + b.BlogUrl)
	if err != nil {
		return "", err
	}
	if resp.StatusCode() != 200 {
		return "", fmt.Errorf("failed to get blog id: %s", resp.String())
	}
	// Get the key "id" from the response
	result := (*resp.Result().(*map[string]interface{}))
	id, ok := result["id"]
	if !ok {
		return "", fmt.Errorf("id not found in response")
	}
	return id.(string), nil
}
func (b Blogger) Pull(options PlatformOptions) (PostData, error) {
	log.Info("Blogger pull called", "options", options)
	postPath := strings.Replace(options.PostUrl, b.BlogUrl, "", 1)
	client := resty.New()
	resp, err := client.R().SetHeader("Authorization", fmt.Sprintf("Bearer %s", options.AccessToken)).SetResult(&map[string]interface{}{}).Get("https://www.googleapis.com/blogger/v3/blogs/" + options.BlogId + "/posts/bypath?path=" + postPath)
	if err != nil {
		return PostData{}, err
	}
	if resp.StatusCode() != 200 {
		return PostData{}, fmt.Errorf("failed to get post: %s", resp.String())
	}
	// Get the keys "title" and "content" from the response
	result := (*resp.Result().(*map[string]interface{}))
	title, ok := result["title"].(string)
	if !ok {
		return PostData{}, fmt.Errorf("title not found in response or is not a string")
	}
	html, ok := result["content"].(string)
	if !ok {
		return PostData{}, fmt.Errorf("content not found in response or is not a string")
	}
	canonicalUrl, ok := result["url"].(string)
	if !ok {
		return PostData{}, fmt.Errorf("url not found in response or is not a string")
	}
	// Convert the HTML to Markdown
	markdown, err := md.NewConverter("", true, nil).ConvertString(html)
	if err != nil {
		return PostData{}, err
	}
	return PostData{
		Title:        title,
		Html:         html,
		Markdown:     markdown,
		CanonicalUrl: canonicalUrl,
	}, nil

}
func (b Blogger) Push(data PostData, options PlatformOptions) error {
	log.Error("not implemented")
	return nil
}
func (b Blogger) GetName() string { return b.Name }
func (b Blogger) GetType() string { return "blogger" }

type Markdown struct {
	Name       string
	ContentDir string
}

func (m Markdown) GetName() string { return m.Name }
func (m Markdown) GetType() string { return "markdown" }

// Push the data to the contentdir with the title as the filename using gosimple/slug.
// The markdown file should have YAML frontmatter compatible with Hugo.
func (m Markdown) Push(data PostData, options PlatformOptions) error {
	// Create the file, if it exists, log an error and return
	fs := afero.NewOsFs()
	slug := slug.Make(data.Title)
	// Clean the slug to remove any characters that may cause issues with the filesystem
	slug = filepath.Clean(slug)
	filePath := filepath.Join(m.ContentDir, slug+".md")
	// Create parent directories if they don't exist
	dirPath := filepath.Dir(filePath)
	if _, err := fs.Stat(dirPath); os.IsNotExist(err) {
		errDir := fs.MkdirAll(dirPath, 0755)
		if errDir != nil {
			log.Error("failed to create directory", "directory", dirPath)
			return errDir
		}
	}
	// Check if the file already exists
	if _, err := fs.Stat(filePath); err == nil {
		log.Error("file already exists", "file", filePath)
		return nil
	}
	// Create the file
	file, err := fs.Create(filePath)
	if err != nil {
		return err
	}
	// After the function returns, close the file
	defer file.Close()
	// Create the frontmatter
	frontmatter := struct {
		Title        string `yaml:"title"`
		CanonicalUrl string `yaml:"canonicalUrl"`
	}{
		Title:        data.Title,
		CanonicalUrl: data.CanonicalUrl,
	}
	// Convert the frontmatter to YAML
	frontmatterYaml, err := yaml.Marshal(frontmatter)
	if err != nil {
		return err
	}
	content := fmt.Sprintf("---\n%s---\n\n%s", frontmatterYaml, data.Markdown)
	log.Debug("Writing content", "content", content, "file", filePath)
	_, err = file.WriteString(content)
	if err != nil {
		return err
	}
	return nil

}
func (m Markdown) Pull(options PlatformOptions) (PostData, error) {
	log.Info("Markdown pull called", "options", options)
	return PostData{}, nil
}

func CreateDestination(destMap map[string]interface{}) (Destination, error) {
	switch destMap["type"] {
	case "blogger":
		return Blogger{
			Name:    destMap["name"].(string),
			BlogUrl: destMap["blog_url"].(string),
		}, nil
	case "markdown":
		return Markdown{
			Name:       destMap["name"].(string),
			ContentDir: destMap["content_dir"].(string),
		}, nil
	default:
		return nil, fmt.Errorf("unknown destination type: %s", destMap["type"])
	}
}

func CreateSource(sourceMap map[string]interface{}) (Source, error) {
	switch sourceMap["type"] {
	case "blogger":
		return Blogger{
			Name:    sourceMap["name"].(string),
			BlogUrl: sourceMap["blog_url"].(string),
		}, nil
	case "file":
		return Markdown{
			Name:       sourceMap["name"].(string),
			ContentDir: sourceMap["content_dir"].(string),
		}, nil
	default:
		return nil, fmt.Errorf("unknown source type: %s", sourceMap["type"])
	}
}
