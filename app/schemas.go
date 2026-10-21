package main

// URLCreate is the request body for POST /shorten.
type URLCreate struct {
	LongURL string `json:"long_url"`
}

// URLResponse is the response body for POST /shorten.
type URLResponse struct {
	ShortCode string `json:"short_code"`
	LongURL   string `json:"long_url"`
}
