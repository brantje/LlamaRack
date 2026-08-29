package api

import (
	"errors"
	"net/url"
	"strings"
)

func validateOIDCFrontendURL(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return errors.New("frontend URL must be an absolute HTTP(S) URL")
	}
	return nil
}

func oidcFrontendExchangeURL(exchangeURL, frontendURL string) (string, error) {
	frontendURL = strings.TrimSpace(frontendURL)
	if frontendURL == "" {
		return exchangeURL, nil
	}
	if err := validateOIDCFrontendURL(frontendURL); err != nil {
		return "", err
	}
	exchange, err := url.Parse(exchangeURL)
	if err != nil {
		return "", err
	}
	code := exchange.Query().Get("oidc_exchange")
	if code == "" {
		return "", errors.New("OIDC exchange redirect is missing exchange code")
	}
	frontend, err := url.Parse(frontendURL)
	if err != nil {
		return "", err
	}
	query := frontend.Query()
	query.Set("oidc_exchange", code)
	frontend.RawQuery = query.Encode()
	frontend.Fragment = ""
	return frontend.String(), nil
}
