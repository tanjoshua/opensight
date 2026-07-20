package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// LocationFromBusinessJSON converts businesses.location into the OpenAI web
// search user_location. It intentionally has no market default; missing country
// is a profile error, not an execution fallback.
func LocationFromBusinessJSON(raw json.RawMessage) (Location, error) {
	if len(raw) == 0 {
		return Location{}, errors.New("business location is required")
	}

	var profile struct {
		Country  string `json:"country"`
		City     string `json:"city"`
		Region   string `json:"region"`
		Area     string `json:"area"`
		Timezone string `json:"timezone"`
	}
	if err := json.Unmarshal(raw, &profile); err != nil {
		return Location{}, fmt.Errorf("parse business location: %w", err)
	}

	location := Location{
		Country:  strings.ToUpper(strings.TrimSpace(profile.Country)),
		City:     strings.TrimSpace(profile.City),
		Region:   strings.TrimSpace(profile.Region),
		Timezone: strings.TrimSpace(profile.Timezone),
	}
	if location.Region == "" {
		location.Region = strings.TrimSpace(profile.Area)
	}
	if location.Country == "" {
		return Location{}, errors.New("business location country is required")
	}
	if !isTwoLetterCountry(location.Country) {
		return Location{}, fmt.Errorf("business location country must be a two-letter ISO code; got %q", location.Country)
	}
	return location, nil
}

func (l Location) validate() error {
	if strings.TrimSpace(l.Country) == "" {
		return errors.New("location country is required")
	}
	if !isTwoLetterCountry(strings.ToUpper(strings.TrimSpace(l.Country))) {
		return fmt.Errorf("location country must be a two-letter ISO code; got %q", l.Country)
	}
	return nil
}

func (l Location) normalized() Location {
	return Location{
		Country:  strings.ToUpper(strings.TrimSpace(l.Country)),
		City:     strings.TrimSpace(l.City),
		Region:   strings.TrimSpace(l.Region),
		Timezone: strings.TrimSpace(l.Timezone),
	}
}

func isTwoLetterCountry(country string) bool {
	if len(country) != 2 {
		return false
	}
	for _, r := range country {
		if r > unicode.MaxASCII || !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}
