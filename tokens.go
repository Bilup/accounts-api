package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

type TokenPermission string

const (
	PermDeleteAccount     TokenPermission = "account:delete"
	PermManageProfile     TokenPermission = "account:profile"
	PermManageSettings    TokenPermission = "account:settings"
	PermViewProfile       TokenPermission = "account:view"
	PermViewCredits       TokenPermission = "credits:view"
	PermManageCredits     TokenPermission = "credits:manage"
	PermTransferCredits   TokenPermission = "credits:transfer"
	PermClaimDaily        TokenPermission = "credits:daily"
	PermViewFriends       TokenPermission = "friends:view"
	PermManageFriends     TokenPermission = "friends:manage"
	PermSendFriendReq     TokenPermission = "friends:request"
	PermAcceptFriend      TokenPermission = "friends:accept"
	PermRemoveFriend      TokenPermission = "friends:remove"
	PermCancelFriendReq   TokenPermission = "friends:cancel"
	PermViewPosts         TokenPermission = "posts:view"
	PermCreatePost        TokenPermission = "posts:create"
	PermDeletePost        TokenPermission = "posts:delete"
	PermManagePosts       TokenPermission = "posts:manage"
	PermLikePost          TokenPermission = "posts:like"
	PermReplyPost         TokenPermission = "posts:reply"
	PermRepost            TokenPermission = "posts:repost"
	PermViewFollowing     TokenPermission = "following:view"
	PermFollow            TokenPermission = "following:follow"
	PermUnfollow          TokenPermission = "following:unfollow"
	PermViewFiles         TokenPermission = "files:view"
	PermManageFiles       TokenPermission = "files:manage"
	PermDeleteFiles       TokenPermission = "files:delete"
	PermViewStorage       TokenPermission = "storage:view"
	PermManageStorage     TokenPermission = "storage:manage"
	PermDeleteStorage     TokenPermission = "storage:delete"
	PermViewKeys          TokenPermission = "keys:view"
	PermManageKeys        TokenPermission = "keys:manage"
	PermViewGroups        TokenPermission = "groups:view"
	PermManageGroups      TokenPermission = "groups:manage"
	PermJoinGroup         TokenPermission = "groups:join"
	PermLeaveGroup        TokenPermission = "groups:leave"
	PermViewGroupMembers  TokenPermission = "groups:members.view"
	PermInviteGroup       TokenPermission = "groups:invite"
	PermBanGroup          TokenPermission = "groups:ban"
	PermViewNotifications TokenPermission = "notifications:view"
	PermSendNotifications TokenPermission = "notifications:send"
	PermViewGifts         TokenPermission = "gifts:view"
	PermCreateGift        TokenPermission = "gifts:create"
	PermClaimGift         TokenPermission = "gifts:claim"
	PermCancelGift        TokenPermission = "gifts:cancel"
	PermViewItems         TokenPermission = "items:view"
	PermBuyItems          TokenPermission = "items:buy"
	PermSellItems         TokenPermission = "items:sell"
	PermManageItems       TokenPermission = "items:manage"
	PermGenerateValidator TokenPermission = "validators:generate"
	PermViewBlocked       TokenPermission = "blocked:view"
	PermManageBlocked     TokenPermission = "blocked:manage"
	PermManageTokens      TokenPermission = "tokens:manage"
	PermViewCosmetics     TokenPermission = "cosmetics:view"
	PermBuyCosmetics      TokenPermission = "cosmetics:buy"
	PermEquipCosmetics    TokenPermission = "cosmetics:equip"
	PermGiftCosmetics     TokenPermission = "cosmetics:gift"
)

func AllPermissions() []TokenPermission {
	return []TokenPermission{
		PermDeleteAccount,
		PermManageProfile,
		PermManageSettings,
		PermViewProfile,
		PermViewCredits,
		PermManageCredits,
		PermTransferCredits,
		PermClaimDaily,
		PermViewFriends,
		PermManageFriends,
		PermSendFriendReq,
		PermAcceptFriend,
		PermRemoveFriend,
		PermCancelFriendReq,
		PermViewPosts,
		PermCreatePost,
		PermDeletePost,
		PermManagePosts,
		PermLikePost,
		PermReplyPost,
		PermRepost,
		PermViewFollowing,
		PermFollow,
		PermUnfollow,
		PermViewFiles,
		PermManageFiles,
		PermDeleteFiles,
		PermViewStorage,
		PermManageStorage,
		PermDeleteStorage,
		PermViewKeys,
		PermManageKeys,
		PermViewGroups,
		PermManageGroups,
		PermJoinGroup,
		PermLeaveGroup,
		PermViewGroupMembers,
		PermInviteGroup,
		PermViewNotifications,
		PermSendNotifications,
		PermViewGifts,
		PermCreateGift,
		PermClaimGift,
		PermCancelGift,
		PermViewItems,
		PermBuyItems,
		PermSellItems,
		PermManageItems,
		PermGenerateValidator,
		PermViewBlocked,
		PermManageBlocked,
		PermManageTokens,
		PermViewCosmetics,
		PermBuyCosmetics,
		PermEquipCosmetics,
		PermGiftCosmetics,
	}
}

type PermissionGroup struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Permissions []TokenPermission `json:"permissions"`
}

func PermissionGroups() []PermissionGroup {
	return []PermissionGroup{
		{
			Name:        "read_only",
			Description: "Read-only access to your profile, posts, friends, and followers",
			Permissions: []TokenPermission{
				PermViewProfile, PermViewCredits, PermViewFriends,
				PermViewPosts, PermViewFollowing, PermViewFiles, PermViewStorage,
				PermViewKeys, PermViewGroups, PermViewGroupMembers, PermViewNotifications,
				PermViewGifts, PermViewItems, PermViewCosmetics, PermViewBlocked,
			},
		},
		{
			Name:        "social",
			Description: "Read and interact with posts, friends, and following",
			Permissions: []TokenPermission{
				PermViewProfile, PermViewCredits, PermViewFriends,
				PermViewPosts, PermCreatePost, PermDeletePost, PermManagePosts,
				PermLikePost, PermReplyPost, PermRepost,
				PermViewFollowing, PermFollow, PermUnfollow,
				PermManageFriends, PermSendFriendReq, PermAcceptFriend,
				PermRemoveFriend, PermCancelFriendReq, PermViewNotifications,
			},
		},
		{
			Name:        "economy",
			Description: "Manage credits, gifts, and marketplace items",
			Permissions: []TokenPermission{
				PermViewProfile, PermViewCredits, PermManageCredits,
				PermTransferCredits, PermClaimDaily,
				PermViewGifts, PermCreateGift, PermClaimGift, PermCancelGift,
				PermViewItems, PermBuyItems, PermSellItems, PermManageItems,
				PermViewCosmetics, PermBuyCosmetics, PermEquipCosmetics, PermGiftCosmetics,
			},
		},
		{
			Name:        "storage",
			Description: "Manage files and storage",
			Permissions: []TokenPermission{
				PermViewProfile, PermViewFiles, PermManageFiles, PermDeleteFiles,
				PermViewStorage, PermManageStorage, PermDeleteStorage,
			},
		},
		{
			Name:        "full",
			Description: "Full access to everything except account deletion and token management",
			Permissions: func() []TokenPermission {
				perms := make([]TokenPermission, 0)
				for _, p := range AllPermissions() {
					if p != PermDeleteAccount && p != PermManageTokens {
						perms = append(perms, p)
					}
				}
				return perms
			}(),
		},
	}
}

type SubToken struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Token       string            `json:"token"`
	Permissions []TokenPermission `json:"permissions"`
	CreatedAt   int64             `json:"created_at"`
	LastUsedAt  *int64            `json:"last_used_at,omitempty"`
	ExpiresAt   *int64            `json:"expires_at,omitempty"`
	Revoked     bool              `json:"revoked"`
	RevokedAt   *int64            `json:"revoked_at,omitempty"`
	Origin      string            `json:"origin,omitempty"`
	Description string            `json:"description,omitempty"`
	Websites    []string          `json:"websites,omitempty"`
}

type TokenStore struct {
	Tokens    []SubToken `json:"tokens"`
	UpdatedAt int64      `json:"updated_at"`
}

var (
	tokenStoreCache = make(map[UserId]*TokenStore)
	// tokenStoreMutex guards tokenStoreCache and the contents of every cached
	// TokenStore. All reads and writes of token data must go through
	// readTokenStore / modifyTokenStore.
	tokenStoreMutex sync.Mutex
)

// loadTokenStoreLocked returns the user's cached store, loading it from disk
// on first access. Caller must hold tokenStoreMutex.
func loadTokenStoreLocked(userId UserId) (*TokenStore, error) {
	if cached, ok := tokenStoreCache[userId]; ok {
		return cached, nil
	}
	store, err := LoadUserJSON[TokenStore](userId, "tokens.json")
	if err != nil {
		if os.IsNotExist(err) {
			fresh := &TokenStore{
				Tokens:    []SubToken{},
				UpdatedAt: time.Now().UnixMilli(),
			}
			tokenStoreCache[userId] = fresh
			return fresh, nil
		}
		return nil, fmt.Errorf("failed to read token store: %w", err)
	}
	tokenStoreCache[userId] = &store
	return &store, nil
}

// readTokenStore returns a snapshot copy of the user's sub-tokens.
func readTokenStore(userId UserId) ([]SubToken, error) {
	tokenStoreMutex.Lock()
	defer tokenStoreMutex.Unlock()
	store, err := loadTokenStoreLocked(userId)
	if err != nil {
		return nil, err
	}
	out := make([]SubToken, len(store.Tokens))
	copy(out, store.Tokens)
	return out, nil
}

// modifyTokenStore runs fn on the user's store under the lock; if fn returns
// true the store is persisted to disk (from a snapshot, outside the lock).
func modifyTokenStore(userId UserId, fn func(*TokenStore) bool) error {
	tokenStoreMutex.Lock()
	store, err := loadTokenStoreLocked(userId)
	if err != nil {
		tokenStoreMutex.Unlock()
		return err
	}
	if !fn(store) {
		tokenStoreMutex.Unlock()
		return nil
	}
	store.UpdatedAt = time.Now().UnixMilli()
	snapshot := TokenStore{
		Tokens:    make([]SubToken, len(store.Tokens)),
		UpdatedAt: store.UpdatedAt,
	}
	copy(snapshot.Tokens, store.Tokens)
	tokenStoreMutex.Unlock()

	return SaveUserJSON(userId, "tokens.json", &snapshot)
}

func generateSubTokenID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate sub-token id: " + err.Error())
	}
	return "st_" + base64.URLEncoding.EncodeToString(b)
}

func generateSubTokenValue() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate sub-token value: " + err.Error())
	}
	return "rotur_st_" + base64.URLEncoding.EncodeToString(b)
}

func (t *SubToken) hasPermission(perm TokenPermission) bool {
	for _, p := range t.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

func (t *SubToken) hasAllPermissions(perms []TokenPermission) bool {
	for _, perm := range perms {
		if !t.hasPermission(perm) {
			return false
		}
	}
	return true
}

func (t *SubToken) ToPublic() SubTokenPublic {
	return SubTokenPublic{
		ID:          t.ID,
		Name:        t.Name,
		Permissions: t.Permissions,
		CreatedAt:   t.CreatedAt,
		LastUsedAt:  t.LastUsedAt,
		ExpiresAt:   t.ExpiresAt,
		Token:       t.Token,
		Revoked:     t.Revoked,
		RevokedAt:   t.RevokedAt,
		Origin:      t.Origin,
		Description: t.Description,
		Websites:    t.Websites,
	}
}

type SubTokenPublic struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Permissions []TokenPermission `json:"permissions"`
	CreatedAt   int64             `json:"created_at"`
	LastUsedAt  *int64            `json:"last_used_at,omitempty"`
	ExpiresAt   *int64            `json:"expires_at,omitempty"`
	Revoked     bool              `json:"revoked"`
	Token       string            `json:"token"`
	RevokedAt   *int64            `json:"revoked_at,omitempty"`
	Origin      string            `json:"origin,omitempty"`
	Description string            `json:"description,omitempty"`
	Websites    []string          `json:"websites,omitempty"`
}

type SubTokenCreate struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Token       string            `json:"token"`
	Permissions []TokenPermission `json:"permissions"`
	CreatedAt   int64             `json:"created_at"`
	ExpiresAt   *int64            `json:"expires_at,omitempty"`
	Origin      string            `json:"origin,omitempty"`
	Description string            `json:"description,omitempty"`
	Websites    []string          `json:"websites,omitempty"`
}

var (
	subTokenIndex      = make(map[string]*subTokenEntry)
	subTokenIndexMutex sync.RWMutex
)

type subTokenEntry struct {
	UserId  UserId
	TokenID string
}

func buildSubTokenIndex() {
	usersMutex.RLock()
	userIds := make([]UserId, 0, len(users))
	for i := range users {
		userIds = append(userIds, users[i].GetId())
	}
	usersMutex.RUnlock()

	idx := make(map[string]*subTokenEntry)
	now := time.Now().UnixMilli()
	for _, userId := range userIds {
		tokens, err := readTokenStore(userId)
		if err != nil {
			continue
		}
		for _, t := range tokens {
			if !t.Revoked && (t.ExpiresAt == nil || *t.ExpiresAt > now) {
				idx[t.Token] = &subTokenEntry{
					UserId:  userId,
					TokenID: t.ID,
				}
			}
		}
	}

	subTokenIndexMutex.Lock()
	subTokenIndex = idx
	subTokenIndexMutex.Unlock()

	log.Printf("Built sub-token index with %d active tokens", len(idx))
}

func authenticateWithSubTokenFast(tokenValue string) (*User, *SubToken, error) {
	subTokenIndexMutex.RLock()
	entry, ok := subTokenIndex[tokenValue]
	subTokenIndexMutex.RUnlock()

	if !ok {
		return nil, nil, fmt.Errorf("sub-token not found")
	}

	// Look up user directly by UserId instead of linear scan by username.
	foundUser, err := getAccountByUserId(entry.UserId)
	if err != nil {
		return nil, nil, fmt.Errorf("user not found for sub-token")
	}
	foundUserPtr := &foundUser

	var matched *SubToken
	var authErr error
	err = modifyTokenStore(entry.UserId, func(store *TokenStore) bool {
		for j := range store.Tokens {
			t := &store.Tokens[j]
			if t.ID != entry.TokenID || t.Token != tokenValue {
				continue
			}
			if t.Revoked {
				authErr = fmt.Errorf("token has been revoked")
				return false
			}
			if t.ExpiresAt != nil && *t.ExpiresAt < time.Now().UnixMilli() {
				authErr = fmt.Errorf("token has expired")
				return false
			}
			now := time.Now().UnixMilli()
			t.LastUsedAt = &now
			tCopy := *t
			matched = &tCopy
			return true
		}
		authErr = fmt.Errorf("sub-token not found in store")
		return false
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load token store: %w", err)
	}
	if matched == nil {
		removeFromSubTokenIndex(tokenValue)
		return nil, nil, authErr
	}
	return foundUserPtr, matched, nil
}

func addToSubTokenIndex(tokenValue string, userId UserId, tokenID string) {
	subTokenIndexMutex.Lock()
	defer subTokenIndexMutex.Unlock()
	subTokenIndex[tokenValue] = &subTokenEntry{
		UserId:  userId,
		TokenID: tokenID,
	}
}

func removeFromSubTokenIndex(tokenValue string) {
	subTokenIndexMutex.Lock()
	defer subTokenIndexMutex.Unlock()
	delete(subTokenIndex, tokenValue)
}

func cleanExpiredSubTokens() {
	for {
		time.Sleep(1 * time.Hour)

		usersMutex.RLock()
		userIds := make([]UserId, 0)
		for i := range users {
			userIds = append(userIds, users[i].GetId())
		}
		usersMutex.RUnlock()

		now := time.Now().UnixMilli()
		for _, userId := range userIds {
			_ = modifyTokenStore(userId, func(store *TokenStore) bool {
				changed := false
				for j := range store.Tokens {
					t := &store.Tokens[j]
					if !t.Revoked && t.ExpiresAt != nil && *t.ExpiresAt < now {
						t.Revoked = true
						revokedAt := now
						t.RevokedAt = &revokedAt
						removeFromSubTokenIndex(t.Token)
						changed = true
					}
				}
				return changed
			})
		}
	}
}
