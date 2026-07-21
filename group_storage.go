package main

import (
	"claw/internal/config"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

func groupDirPath(groupTag string) string {
	groupsDataMutex.RLock()
	data, ok := groupsData[groupTag]
	groupsDataMutex.RUnlock()
	if ok && data != nil && data.Group.Id != "" {
		return filepath.Join(config.GROUPS_FILE_PATH, string(data.Group.Id))
	}
	return filepath.Join(config.GROUPS_FILE_PATH, "unknown_"+groupTag)
}

func getGroupBannerPath(groupTag string) string {
	dirPath := groupDirPath(groupTag)
	for _, ext := range []string{".jpg", ".png", ".gif"} {
		p := filepath.Join(dirPath, "banner"+ext)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func getGroupIconPath(groupTag string) string {
	dirPath := groupDirPath(groupTag)
	for _, ext := range []string{".jpg", ".png", ".gif"} {
		p := filepath.Join(dirPath, "icon"+ext)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func loadGroupTips(groupTag string) []GroupTip {
	return loadJSONOrDefault(filepath.Join(groupDirPath(groupTag), "tips.json"), []GroupTip{})
}

func loadGroupWithdrawals(groupTag string) []GroupTipWithdrawal {
	return loadJSONOrDefault(filepath.Join(groupDirPath(groupTag), "withdrawals.json"), []GroupTipWithdrawal{})
}

func saveGroupWithdrawals(groupTag string, withdrawals []GroupTipWithdrawal) {
	dirPath := groupDirPath(groupTag)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		log.Printf("Error creating group directory for withdrawals %s: %v", groupTag, err)
		return
	}
	withdrawalsPath := filepath.Join(dirPath, "withdrawals.json")
	data, err := json.MarshalIndent(withdrawals, "", " ")
	if err != nil {
		log.Printf("Error marshaling withdrawals for %s: %v", groupTag, err)
		return
	}
	if err := atomicWrite(withdrawalsPath, data, 0644); err != nil {
		log.Printf("Error saving withdrawals for %s: %v", groupTag, err)
	}
}

func addGroupWithdrawal(groupTag string, withdrawal GroupTipWithdrawal) bool {
	groupsDataMutex.Lock()
	data, ok := groupsData[groupTag]
	if !ok || data == nil || data.Group.CreditsBalance < withdrawal.AmountCredits {
		groupsDataMutex.Unlock()
		return false
	}
	data.Group.CreditsBalance = roundVal(data.Group.CreditsBalance - withdrawal.AmountCredits)
	groupsDataMutex.Unlock()

	withdrawals := loadGroupWithdrawals(groupTag)
	withdrawals = append(withdrawals, withdrawal)
	go saveGroupWithdrawals(groupTag, withdrawals)
	go saveGroupData(groupTag)
	return true
}

func saveGroupTips(groupTag string, tips []GroupTip) {
	dirPath := groupDirPath(groupTag)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		log.Printf("Error creating group directory for tips %s: %v", groupTag, err)
		return
	}
	tipsPath := filepath.Join(dirPath, "tips.json")
	data, err := json.MarshalIndent(tips, "", " ")
	if err != nil {
		log.Printf("Error marshaling tips for %s: %v", groupTag, err)
		return
	}
	if err := atomicWrite(tipsPath, data, 0644); err != nil {
		log.Printf("Error saving tips for %s: %v", groupTag, err)
	}
}
