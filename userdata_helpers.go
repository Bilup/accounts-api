package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func getUserDataDir(userId UserId) string {
	return filepath.Join(USERDATA_PATH, string(userId))
}

func getUserDataFile(userId UserId, filename string) string {
	return filepath.Join(getUserDataDir(userId), filename)
}

func LoadUserJSON[T any](userId UserId, filename string) (T, error) {
	var result T
	path := getUserDataFile(userId, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, fmt.Errorf("unmarshaling %s: %w", path, err)
	}
	return result, nil
}

func SaveUserJSON[T any](userId UserId, filename string, v T) error {
	path := getUserDataFile(userId, filename)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating user data directory %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", path, err)
	}
	return atomicWrite(path, data, 0644)
}

func LoadUserCosmetics(userId UserId) (*UserCosmetics, error) {
	uc, err := LoadUserJSON[UserCosmetics](userId, "cosmetics.json")
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

func SaveUserCosmetics(userId UserId, uc *UserCosmetics) error {
	if uc == nil {
		return fmt.Errorf("nil UserCosmetics provided")
	}
	return SaveUserJSON(userId, "cosmetics.json", *uc)
}
