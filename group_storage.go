package main

import (
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
		return filepath.Join(GROUPS_FILE_PATH, string(data.Group.Id))
	}
	return filepath.Join(GROUPS_FILE_PATH, "unknown_"+groupTag)
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
	dirPath := groupDirPath(groupTag)
	tipsPath := filepath.Join(dirPath, "tips.json")
	data, err := os.ReadFile(tipsPath)
	if err != nil {
		return []GroupTip{}
	}
	var tips []GroupTip
	if err := json.Unmarshal(data, &tips); err != nil {
		return []GroupTip{}
	}
	return tips
}

func loadGroupWithdrawals(groupTag string) []GroupTipWithdrawal {
	dirPath := groupDirPath(groupTag)
	withdrawalsPath := filepath.Join(dirPath, "withdrawals.json")
	data, err := os.ReadFile(withdrawalsPath)
	if err != nil {
		return []GroupTipWithdrawal{}
	}
	var withdrawals []GroupTipWithdrawal
	if err := json.Unmarshal(data, &withdrawals); err != nil {
		return []GroupTipWithdrawal{}
	}
	return withdrawals
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

func addGroupWithdrawal(groupTag string, withdrawal GroupTipWithdrawal) {
	withdrawals := loadGroupWithdrawals(groupTag)
	withdrawals = append(withdrawals, withdrawal)
	groupsDataMutex.Lock()
	data := groupsData[groupTag]
	data.Group.CreditsBalance -= withdrawal.AmountCredits
	groupsData[groupTag] = data
	groupsDataMutex.Unlock()
	go saveGroupWithdrawals(groupTag, withdrawals)
	go saveGroupData(groupTag)
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
