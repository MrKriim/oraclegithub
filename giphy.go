package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// GiphySearchHandler handles requests from HTMX to find GIFs
func GiphySearchHandler(c *fiber.Ctx) error {
	if _, err := requireUser(c); err != nil {
		return err
	}
	query := c.Query("q")
	apiKey := "rXXwud1I2cZvFwFZ0b7Jds49WvuMPdSf"
	url := fmt.Sprintf("https://api.giphy.com/v1/gifs/trending?api_key=%s&limit=15&rating=pg", apiKey)

	if query != "" {
		url = fmt.Sprintf("https://api.giphy.com/v1/gifs/search?api_key=%s&q=%s&limit=15&rating=pg", apiKey, query)
	}

	resp, err := http.Get(url)
	if err != nil {
		return c.SendString("Error")
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			Images struct {
				// FIXED: Use FixedWidthSmall for better grid alignment
				FixedWidthSmall struct {
					URL string `json:"url"`
				} `json:"fixed_width_small"`
			} `json:"images"`
		} `json:"data"`
	}

	json.NewDecoder(resp.Body).Decode(&result)

	var html strings.Builder
	for _, gif := range result.Data {
		u := gif.Images.FixedWidthSmall.URL
		if u == "" {
			continue
		}

		html.WriteString(fmt.Sprintf(`
    <div class="w-full h-64 bg-gray-200 dark:bg-gray-800 rounded-2xl overflow-hidden relative mb-4 flex-shrink-0">
        <img src="%s" 
             onclick="selectGiphy('%s')" 
             class="absolute inset-0 w-full h-full object-cover cursor-pointer active:scale-95 transition-all"
             loading="lazy"
             style="display: block; width: 100%%; min-height: 256px;">
    </div>`, u, u))
	}
	return c.SendString(html.String())

}
