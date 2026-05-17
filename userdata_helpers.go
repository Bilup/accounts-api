package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func getUserDataFile(username, filename string) string {
	return filepath.Join(USERDATA_PATH, strings.ToLower(username), filename)
}

func LoadUserJSON[T any](username, filename string) (T, error) {
	var result T
	path := getUserDataFile(username, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, fmt.Errorf("unmarshaling %s: %w", path, err)
	}
	return result, nil
}

func SaveUserJSON[T any](username, filename string, v T) error {
	path := getUserDataFile(username, filename)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating user data directory %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", path, err)
	}
	return atomicWrite(path, data, 0644)
}

func LoadUserCosmetics(username string) (*UserCosmetics, error) {
	uc, err := LoadUserJSON[UserCosmetics](username, "cosmetics.json")
	if err != nil {
		if os.IsNotExist(err) {
			return &UserCosmetics{ActiveCosmetics: map[CosmeticType]string{}, OwnedCosmetics: []string{}}, nil
		}
		return nil, err
	}
	if uc.ActiveCosmetics == nil {
		uc.ActiveCosmetics = map[CosmeticType]string{}
	}
	if uc.OwnedCosmetics == nil {
		uc.OwnedCosmetics = []string{}
	}
	return &uc, nil
}

func SaveUserCosmetics(username string, uc *UserCosmetics) error {
	if uc == nil {
		return fmt.Errorf("nil UserCosmetics provided")
	}
	return SaveUserJSON(username, "cosmetics.json", *uc)
}
