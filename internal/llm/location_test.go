package llm

import (
	"encoding/json"
	"testing"
)

func TestLocationFromBusinessJSON(t *testing.T) {
	location, err := LocationFromBusinessJSON(json.RawMessage(`{
		"address": "1 Example Road",
		"area": "Novena",
		"city": "Singapore",
		"country": "sg",
		"timezone": "Asia/Singapore"
	}`))
	if err != nil {
		t.Fatalf("LocationFromBusinessJSON returned error: %v", err)
	}

	if location.Country != "SG" {
		t.Errorf("Country = %q, want SG", location.Country)
	}
	if location.City != "Singapore" {
		t.Errorf("City = %q", location.City)
	}
	if location.Region != "Novena" {
		t.Errorf("Region = %q, want area fallback", location.Region)
	}
	if location.Timezone != "Asia/Singapore" {
		t.Errorf("Timezone = %q", location.Timezone)
	}
}

func TestLocationFromBusinessJSONPrefersRegionOverArea(t *testing.T) {
	location, err := LocationFromBusinessJSON(json.RawMessage(`{
		"area": "Novena",
		"region": "Central",
		"country": "SG"
	}`))
	if err != nil {
		t.Fatalf("LocationFromBusinessJSON returned error: %v", err)
	}

	if location.Region != "Central" {
		t.Errorf("Region = %q, want explicit region", location.Region)
	}
}

func TestLocationFromBusinessJSONRequiresCountry(t *testing.T) {
	if _, err := LocationFromBusinessJSON(json.RawMessage(`{"city":"Singapore"}`)); err == nil {
		t.Fatal("expected missing country to return error")
	}
}

func TestLocationFromBusinessJSONRejectsNonISOCountry(t *testing.T) {
	if _, err := LocationFromBusinessJSON(json.RawMessage(`{"country":"Singapore"}`)); err == nil {
		t.Fatal("expected long country to return error")
	}
}
