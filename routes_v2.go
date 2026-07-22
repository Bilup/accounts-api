package main

import "github.com/gin-gonic/gin"

func setupV2Routes(r *gin.Engine) {
	v2 := r.Group("/v2")
	v2.Use(v2BodyToQuery(), v2ParamsToQuery())

	v2.POST("/sessions", createSessionV2)
	v2.GET("/sessions", currentSessionV2)
	v2.DELETE("/sessions", deleteSessionV2)

	v2.GET("/limits", getLimits)
	v2.GET("/status", rateLimit("default"), getStatus)
	v2.GET("/supporters", rateLimit("default"), getSupporters)
	v2.GET("/badges", rateLimit("default"), requiresAuth, requirePermission(PermViewProfile), getBadges)
	v2.GET("/ai", rateLimit("ai"), requiresAuth, requirePermission(PermViewPosts), handleAI)
	v2.GET("/ws", statusWSHandler)
	v2.GET("/claw/ws", clawWSHandler)

	auth := v2.Group("/auth")
	{
		auth.GET("/check", requiresAuth, requirePermission(PermViewProfile), checkAuth)
	}

	me := v2.Group("/me")
	{
		me.GET("", rateLimit("profile"), getUser)
		me.PATCH("", updateUser)
		me.GET("/profile", rateLimit("profile"), getProfile)
		me.GET("/benefits", requiresAuth, requirePermission(PermViewProfile), getUserSubscriptionBenefits)
		me.GET("/abilities", requiresAuth, getTokenAbilities)
		me.POST("/password", requiresAuth, requireMainToken(), changePasswordHandler)
		me.POST("/token/refresh", requiresAuth, requireMainToken(), refreshToken)
		me.POST("/transfers", requiresAuth, requirePermission(PermTransferCredits), transferCredits)
		me.POST("/gamble", requiresAuth, requirePermission(PermManageCredits), gambleCredits)
		me.POST("/verification/resend", requiresAuth, resendVerificationHandler)
		me.POST("/tos", requiresAuth, requirePermission(PermManageSettings), acceptTos)
		me.DELETE("/data", requiresAuth, requirePermission(PermDeleteAccount), deleteUserKey)
		me.GET("/blocked", requiresAuth, requirePermission(PermViewBlocked), getBlocking)
		me.PUT("/blocked/:username", requiresAuth, requirePermission(PermManageBlocked), blockUser)
		me.DELETE("/blocked/:username", requiresAuth, requirePermission(PermManageBlocked), unblockUser)
		me.GET("/notes", requiresAuth, requirePermission(PermManageProfile), requireTier("Plus"), getNotes)
		me.PUT("/notes/:username", requiresAuth, requirePermission(PermManageProfile), requireTier("Plus"), noteUser)
		me.DELETE("/notes/:username", requiresAuth, requirePermission(PermManageProfile), requireTier("Plus"), deleteNote)
		me.GET("/daily", rateLimit("default"), requiresAuth, requirePermission(PermClaimDaily), timeUntilNextClaim)
		me.POST("/daily", rateLimit("default"), requiresAuth, requirePermission(PermClaimDaily), requireStanding(StandingGood), claimDaily)
		me.GET("/scheduled-posts", requiresAuth, getMyScheduled)
		me.GET("/bookmarks", requiresAuth, getBookmarks)
	}

	accounts := v2.Group("/accounts")
	{
		accounts.POST("", rateLimit("register"), registerUser)
		accounts.POST("/password/reset", rateLimit("register"), resetPasswordHandler)
		accounts.POST("/password/reset-request", rateLimit("register"), requestPasswordResetHandler)
		accounts.POST("/deleted-check", checkDeletedAccounts)
		accounts.POST("/banned-check", checkBanned)
		accounts.GET("/verify-email", verifyEmailHandler)
	}

	users := v2.Group("/users")
	{
		users.GET("/:username", rateLimit("profile"), getProfile)
		users.GET("/:username/profile", rateLimit("profile"), getProfile)
		users.GET("/:username/exists", rateLimit("profile"), getExists)
		users.GET("/:username/cosmetics", rateLimit("profile"), getProfileCosmetics)
		users.GET("/:username/followers", rateLimit("profile"), getFollowers)
		users.GET("/:username/following", rateLimit("profile"), getFollowing)
		users.GET("/:username/items", listItems)
		users.PUT("/:username/follow", rateLimit("follow"), requiresAuth, requirePermission(PermFollow), requireStanding(StandingWarning), followUser)
		users.DELETE("/:username/follow", rateLimit("follow"), requiresAuth, requirePermission(PermUnfollow), unfollowUser)
		users.DELETE("/:username", requiresAuth, requirePermission(PermDeleteAccount), deleteUser)
	}
	v2.POST("/profiles/cosmetics", rateLimit("default"), getProfileCosmeticsBatch)

	posts := v2.Group("/posts")
	{
		posts.GET("", rateLimit("default"), getFeed)
		posts.POST("", rateLimit("default"), requiresAuth, requirePermission(PermCreatePost), requireStanding(StandingGood), createPost)
		posts.GET("/top", rateLimit("search"), getTopPosts)
		posts.GET("/search", rateLimit("search"), searchPosts)
		posts.GET("/:id", rateLimit("default"), getPost)
		posts.PATCH("/:id", rateLimit("default"), requiresAuth, requirePermission(PermManagePosts), requireStanding(StandingGood), editPost)
		posts.DELETE("/:id", requiresAuth, requirePermission(PermDeletePost), deletePost)
		posts.POST("/:id/replies", rateLimit("default"), requiresAuth, requirePermission(PermReplyPost), requireStanding(StandingGood), replyToPost)
		posts.PUT("/:id/like", requiresAuth, requirePermission(PermLikePost), v2Like("1"), ratePost)
		posts.DELETE("/:id/like", requiresAuth, requirePermission(PermLikePost), v2Like("0"), ratePost)
		posts.POST("/:id/views", rateLimit("default"), requiresAuth, viewPost)
		posts.POST("/:id/repost", rateLimit("default"), requiresAuth, requirePermission(PermRepost), requireStanding(StandingGood), repost)
		posts.PUT("/:id/pin", requiresAuth, requirePermission(PermManagePosts), pinPost)
		posts.DELETE("/:id/pin", requiresAuth, requirePermission(PermManagePosts), unpinPost)
		posts.POST("/:id/poll/votes", rateLimit("default"), requiresAuth, votePoll)
		posts.PUT("/:id/bookmark", requiresAuth, addBookmark)
		posts.DELETE("/:id/bookmark", requiresAuth, removeBookmark)
	}

	v2.GET("/feed/following", rateLimit("default"), requiresAuth, requirePermission(PermViewPosts), getFollowingFeed)
	v2.GET("/notifications", rateLimit("default"), requiresAuth, requirePermission(PermViewNotifications), getNotifications)

	friends := v2.Group("/friends")
	{
		friends.GET("", requiresAuth, requirePermission(PermViewFriends), getMeFriends)
		friends.GET("/requests", requiresAuth, requirePermission(PermViewFriends), getMeRequests)
		friends.GET("/requests/outgoing", requiresAuth, requirePermission(PermViewFriends), getMeRequestsOut)
		friends.POST("/requests/:username", requiresAuth, requirePermission(PermSendFriendReq), requireStanding(StandingGood), sendFriendRequest)
		friends.POST("/requests/:username/accept", requiresAuth, requirePermission(PermAcceptFriend), acceptFriendRequest)
		friends.POST("/requests/:username/reject", requiresAuth, requirePermission(PermAcceptFriend), rejectFriendRequest)
		friends.DELETE("/requests/:username", requiresAuth, requirePermission(PermCancelFriendReq), requireStanding(StandingGood), cancelFriendRequest)
		friends.DELETE("/:username", requiresAuth, requirePermission(PermRemoveFriend), removeFriend)
	}

	gifts := v2.Group("/gifts")
	{
		gifts.POST("", rateLimit("default"), requiresAuth, requirePermission(PermCreateGift), requireStanding(StandingGood), createGift)
		gifts.GET("/mine", requiresAuth, requirePermission(PermViewGifts), getMyGifts)
		gifts.GET("/:code", getGift)
		gifts.POST("/:code/claim", rateLimit("default"), requiresAuth, requirePermission(PermClaimGift), requireStanding(StandingWarning), claimGift)
		gifts.POST("/cancel/:id", requiresAuth, requirePermission(PermCancelGift), cancelGift)
	}

	cosmetics := v2.Group("/cosmetics")
	{
		cosmetics.GET("", rateLimit("default"), getShop)
		cosmetics.GET("/mine", requiresAuth, requirePermission(PermViewCosmetics), getMyCosmetics)
		cosmetics.GET("/gifts/mine", requiresAuth, requirePermission(PermViewCosmetics), getMyCosmeticGifts)
		cosmetics.POST("/gifts", rateLimit("default"), requiresAuth, requirePermission(PermGiftCosmetics), requireStanding(StandingGood), giftCosmetic)
		cosmetics.GET("/overlays/*filepath", rateLimit("default"), serveOverlayAsset)
		cosmetics.POST("/unequip", requiresAuth, requirePermission(PermEquipCosmetics), unequipCosmetic)
		cosmetics.GET("/:id", rateLimit("default"), getCosmeticDetail)
		cosmetics.POST("/:id/purchase", rateLimit("default"), requiresAuth, requirePermission(PermBuyCosmetics), requireStanding(StandingWarning), purchaseCosmetic)
		cosmetics.POST("/:id/equip", requiresAuth, requirePermission(PermEquipCosmetics), equipCosmetic)
		cosmetics.GET("/admin", rateLimit("default"), adminListCosmetics)
		cosmetics.POST("/admin", rateLimit("default"), adminCreateCosmetic)
		cosmetics.PATCH("/admin/:id", rateLimit("default"), adminUpdateCosmetic)
		cosmetics.DELETE("/admin/:id", rateLimit("default"), adminDeleteCosmetic)
	}

	items := v2.Group("/items")
	{
		items.POST("", requiresAuth, requirePermission(PermManageItems), requireStanding(StandingGood), createItem)
		items.GET("/selling", getSellingItems)
		items.POST("/admin-add/:id", requiresAuth, requirePermission(PermManageItems), adminAddUserToItem)
		items.GET("/:name", getItem)
		items.POST("/:name/buy", requiresAuth, requirePermission(PermBuyItems), requireStanding(StandingWarning), buyItem)
		items.POST("/:name/transfer", requiresAuth, requirePermission(PermManageItems), requireStanding(StandingGood), transferItem)
		items.POST("/:name/sell", requiresAuth, requirePermission(PermSellItems), requireStanding(StandingGood), sellItem)
		items.POST("/:name/stop-selling", requiresAuth, requirePermission(PermSellItems), stopSellingItem)
		items.PATCH("/:name/price", requiresAuth, requirePermission(PermManageItems), setItemPrice)
		items.PATCH("/:name", requiresAuth, requirePermission(PermManageItems), updateItem)
		items.DELETE("/:name", requiresAuth, requirePermission(PermManageItems), deleteItem)
	}

	keys := v2.Group("/keys")
	{
		keys.POST("", requiresAuth, requirePermission(PermManageKeys), createKey)
		keys.GET("/mine", requiresAuth, requirePermission(PermViewKeys), getMyKeys)
		keys.GET("/check/:username", checkKey)
		keys.GET("/debug/subscriptions", requiresAuth, requirePermission(PermViewKeys), debugSubscriptionsEndpoint)
		keys.GET("/:id", getKey)
		keys.PATCH("/:id", requiresAuth, requirePermission(PermManageKeys), updateKey)
		keys.PATCH("/:id/name", requiresAuth, requirePermission(PermManageKeys), setKeyName)
		keys.DELETE("/:id", requiresAuth, requirePermission(PermManageKeys), deleteKey)
		keys.POST("/:id/revoke", requiresAuth, requirePermission(PermManageKeys), revokeKey)
		keys.POST("/:id/buy", requiresAuth, requirePermission(PermManageKeys), buyKey)
		keys.POST("/:id/cancel", requiresAuth, requirePermission(PermManageKeys), cancelKey)
		keys.POST("/:id/members", requiresAuth, requirePermission(PermManageKeys), adminAddUserToKey)
		keys.DELETE("/:id/members", requiresAuth, requirePermission(PermManageKeys), adminRemoveUserFromKey)
	}

	tokens := v2.Group("/tokens")
	{
		tokens.GET("/permissions", listPermissions)
		tokens.GET("", requiresAuth, requirePermission(PermManageTokens), listSubTokens)
		tokens.GET("/active", requiresAuth, requirePermission(PermManageTokens), listActiveSubTokens)
		tokens.POST("", requiresAuth, requireMainToken(), createSubToken)
		tokens.GET("/:id", requiresAuth, getSubToken)
		tokens.GET("/:id/activity", requiresAuth, requirePermission(PermManageTokens), getSubTokenActivity)
		tokens.PATCH("/:id", requiresAuth, requireMainToken(), updateSubToken)
		tokens.POST("/:id/rename", requiresAuth, requireMainToken(), renameSubToken)
		tokens.POST("/:id/revoke", requiresAuth, requireMainToken(), revokeSubToken)
		tokens.DELETE("/:id", requiresAuth, requireMainToken(), deleteSubToken)
	}

	files := v2.Group("/files")
	{
		files.GET("", requiresAuth, requirePermission(PermViewFiles), getFileByUUID)
		files.POST("", requiresAuth, requirePermission(PermManageFiles), updateFiles)
		files.DELETE("", requiresAuth, requirePermission(PermDeleteFiles), deleteAllUserFiles)
		files.GET("/usage", requiresAuth, requirePermission(PermViewFiles), getUserFileSize)
		files.GET("/index", requiresAuth, requirePermission(PermViewFiles), getFilesIndex)
		files.GET("/entries", requiresAuth, requirePermission(PermViewFiles), getFilesAll)
		files.POST("/by-uuid", requiresAuth, requirePermission(PermViewFiles), getFilesByUUIDs)
		files.GET("/by-uuid", requiresAuth, requirePermission(PermViewFiles), getFileByUUID)
		files.GET("/by-path/*path", requiresAuth, requirePermission(PermViewFiles), getFileByPath)
		files.POST("/stats", requiresAuth, requirePermission(PermViewFiles), getFileSizes)
		files.GET("/path-index", requiresAuth, requirePermission(PermViewFiles), getPathIndex)
	}

	storage := v2.Group("/storage")
	{
		storage.GET("", requiresAuth, requirePermission(PermViewStorage), listStorage)
		storage.DELETE("", requiresAuth, requirePermission(PermDeleteStorage), clearStorage)
		storage.GET("/usage", requiresAuth, requirePermission(PermViewStorage), getStorageUsage)
		storage.GET("/:id", requiresAuth, requirePermission(PermViewStorage), getStorage)
		storage.PUT("/:id", requiresAuth, requirePermission(PermManageStorage), setStorage)
		storage.DELETE("/:id", requiresAuth, requirePermission(PermDeleteStorage), deleteStorage)
	}

	validators := v2.Group("/validators")
	{
		validators.POST("", requiresAuth, requirePermission(PermGenerateValidator), generateValidator)
		validators.GET("/verify", validateToken)
	}

	link := v2.Group("/link")
	{
		link.GET("/code", rateLimit("default"), getLinkCode)
		link.GET("/status", rateLimit("default"), getLinkStatus)
		link.GET("/user", rateLimit("default"), getLinkedUser)
		link.POST("/code", requiresAuth, requirePermission(PermManageSettings), linkCodeToAccount)
	}

	systems := v2.Group("/systems")
	{
		systems.GET("", getSystems)
		systems.GET("/users", requiresAuth, requirePermission(PermViewGroups), getSystemUsers)
		systems.POST("/reload", requiresAuth, requirePermission(PermManageSettings), reloadSystemsEndpoint)
		systems.PATCH("", requiresAuth, requirePermission(PermManageSettings), updateSystem)
	}

	v2.GET("/status/live", statusGetHTTP)
	v2.PUT("/status/live", requiresAuth, statusSetHTTP)
	v2.GET("/status/ws", statusWSHandler)

	stats := v2.Group("/stats")
	{
		stats.GET("/economy", rateLimit("default"), getEconomyStats)
		stats.GET("/users", rateLimit("default"), getUserStats)
		stats.GET("/most-gained", rateLimit("default"), getMostGained)
		stats.GET("/systems", rateLimit("default"), getSystemStats)
		stats.GET("/followers", rateLimit("default"), getFollowersStats)
		stats.GET("/posts", rateLimit("default"), getPostStats)
	}

	v2.GET("/standing", GetUserStanding)
	v2.POST("/reports", rateLimit("default"), requiresAuth, submitReport)

	mod := v2.Group("/mod")
	{
		mod.GET("/reports", rateLimit("default"), requiresAuth, requireModerator, modListReports)
		mod.POST("/reports/resolve", rateLimit("default"), requiresAuth, requireModerator, modResolveReport)
		mod.POST("/standing", rateLimit("default"), requiresAuth, requireModerator, modSetStanding)
		mod.POST("/standing/recover", rateLimit("default"), requiresAuth, requireModerator, modRecoverStanding)
		mod.POST("/standing/history", rateLimit("default"), requiresAuth, requireModerator, modGetStandingHistory)
		mod.GET("/moderators", rateLimit("default"), requiresAuth, requireModerator, modListModerators)
		mod.POST("/moderators", rateLimit("default"), requiresAuth, requireNetworkAdmin, modSetModerator)
	}

	notify := v2.Group("/notify")
	{
		notify.GET("/vapid", rateLimit("notify"), getVAPIDKeys)
		notify.POST("/register", rateLimit("notify"), requiresAuth, requirePermission(PermManageSettings), registerForNotifications)
		notify.GET("/check", requiresAuth, requirePermission(PermViewNotifications), checkNotifyRegistration)
		notify.GET("/endpoints", requiresAuth, requirePermission(PermViewNotifications), getNotifyEndpoints)
		notify.DELETE("/devices/:device_id", requiresAuth, requirePermission(PermManageSettings), deleteNotifyDevice)
		notify.GET("/allowed", requiresAuth, requirePermission(PermViewNotifications), getNotifyAllowedSenders)
		notify.PUT("/allowed/:username", requiresAuth, requirePermission(PermManageSettings), addNotifyAllowedSender)
		notify.DELETE("/allowed/:username", requiresAuth, requirePermission(PermManageSettings), removeNotifyAllowedSender)
		notify.GET("/log", rateLimit("notify"), requiresAuth, requirePermission(PermViewNotifications), getNotifyLogHandler)
		notify.POST("/users", rateLimit("notify"), requiresAuth, requirePermission(PermSendNotifications), notifyManyUsers)
		notify.POST("/users/:username", rateLimit("notify"), requiresAuth, requirePermission(PermSendNotifications), notifyUser)
		notify.GET("/sources/:source/users", rateLimit("notify"), requiresAuth, requirePermission(PermViewNotifications), getNotifiableUsers)
	}

	devfund := v2.Group("/devfund")
	{
		devfund.POST("/escrow/transfer", requiresAuth, requirePermission(PermTransferCredits), escrowTransfer)
		devfund.POST("/escrow/release", requiresAuth, requirePermission(PermManageCredits), escrowRelease)
		devfund.POST("/escrow/release-service", escrowReleaseService)
	}

	groups := v2.Group("/groups")
	{
		groups.GET("/mine", requiresAuth, requirePermission(PermViewGroups), getMyGroups)
		groups.GET("/search", requiresAuth, requirePermission(PermViewGroups), searchGroups)
		groups.GET("/top", getTopGroups)
		groups.POST("", requiresAuth, requirePermission(PermManageGroups), requireStanding(StandingGood), createGroup)
		groups.GET("/invites/mine", requiresAuth, requirePermission(PermViewGroups), getMyGroupInvites)
		groups.GET("/products/subscriptions/mine", requiresAuth, requirePermission(PermViewGroups), getMyGroupProductSubscriptions)
		groups.GET("/:grouptag", getGroup)
		groups.PATCH("/:grouptag", requiresAuth, requirePermission(PermManageGroups), updateGroup)
		groups.DELETE("/:grouptag", requiresAuth, requirePermission(PermManageGroups), deleteGroup)
		groups.PUT("/:grouptag/represent", requiresAuth, requirePermission(PermManageSettings), representGroup)
		groups.DELETE("/:grouptag/represent", requiresAuth, requirePermission(PermManageSettings), disrepresentGroup)
		groups.POST("/:grouptag/report", requiresAuth, requirePermission(PermViewGroups), reportGroup)
		groups.POST("/:grouptag/join", requiresAuth, requirePermission(PermJoinGroup), joinGroup)
		groups.POST("/:grouptag/leave", requiresAuth, requirePermission(PermLeaveGroup), leaveGroup)
		groups.POST("/:grouptag/icon", requiresAuth, requirePermission(PermManageGroups), uploadGroupIcon)
		groups.GET("/:grouptag/icon.jpg", getGroupIcon)
		groups.POST("/:grouptag/banner", requiresAuth, requirePermission(PermManageGroups), uploadGroupBanner)
		groups.GET("/:grouptag/banner", getGroupBanner)

		groups.GET("/:grouptag/announcements", getAnnouncements)
		groups.POST("/:grouptag/announcements", requiresAuth, requirePermission(PermManageGroups), createAnnouncement)
		groups.DELETE("/:grouptag/announcements/:announcementid", requiresAuth, requirePermission(PermManageGroups), deleteAnnouncement)
		groups.POST("/:grouptag/announcements/mute", requiresAuth, requirePermission(PermManageGroups), toggleAnnouncementMute)

		groups.GET("/:grouptag/events", requiresAuth, requirePermission(PermViewGroups), getEvents)
		groups.POST("/:grouptag/events", requiresAuth, requirePermission(PermManageGroups), createEvent)
		groups.PATCH("/:grouptag/events/:eventid", requiresAuth, requirePermission(PermManageGroups), updateEvent)
		groups.DELETE("/:grouptag/events/:eventid", requiresAuth, requirePermission(PermManageGroups), deleteEvent)

		groups.GET("/:grouptag/tips", requiresAuth, requirePermission(PermViewGroups), getTips)
		groups.POST("/:grouptag/tips", requiresAuth, requirePermission(PermManageCredits), sendTip)
		groups.POST("/:grouptag/tips/withdraw", requiresAuth, requirePermission(PermManageCredits), withdrawTip)
		groups.GET("/:grouptag/tips/withdrawals", requiresAuth, requirePermission(PermViewGroups), getWithdrawals)

		groups.GET("/:grouptag/products", requiresAuth, requirePermission(PermViewGroups), getGroupProducts)
		groups.POST("/:grouptag/products", requiresAuth, requirePermission(PermManageGroups), createGroupProduct)
		groups.DELETE("/:grouptag/products/:productid", requiresAuth, requirePermission(PermManageGroups), deleteGroupProduct)
		groups.POST("/:grouptag/products/:productid/purchase", requiresAuth, requirePermission(PermManageCredits), purchaseGroupProduct)
		groups.POST("/:grouptag/products/:productid/cancel", requiresAuth, requirePermission(PermManageCredits), cancelGroupProductSubscription)
		groups.GET("/:grouptag/products/:productid/owners/:username", checkGroupProductOwnership)

		groups.GET("/:grouptag/roles", requiresAuth, requirePermission(PermViewGroups), getRoles)
		groups.POST("/:grouptag/roles", requiresAuth, requirePermission(PermManageGroups), createRole)
		groups.PATCH("/:grouptag/roles/:roleid", requiresAuth, requirePermission(PermManageGroups), updateRole)
		groups.DELETE("/:grouptag/roles/:roleid", requiresAuth, requirePermission(PermManageGroups), deleteRole)

		groups.GET("/:grouptag/members", requiresAuth, requirePermission(PermViewGroupMembers), getGroupMembersList)
		groups.GET("/:grouptag/members/:userid", requiresAuth, requirePermission(PermViewGroupMembers), getGroupMemberInfo)
		groups.DELETE("/:grouptag/members/:userid", requiresAuth, requirePermission(PermManageGroups), kickGroupMember)
		groups.PUT("/:grouptag/members/:userid/ban", requiresAuth, requirePermission(PermManageGroups), banGroupMember)
		groups.DELETE("/:grouptag/members/:userid/ban", requiresAuth, requirePermission(PermManageGroups), unbanGroupMember)
		groups.GET("/:grouptag/bans", requiresAuth, requirePermission(PermManageGroups), getGroupBans)
		groups.GET("/:grouptag/bans/:userid", requiresAuth, requirePermission(PermViewGroupMembers), checkGroupBan)
		groups.GET("/:grouptag/members/:userid/roles", requiresAuth, requirePermission(PermViewGroups), getUserRoles)
		groups.GET("/:grouptag/members/:userid/permissions", requiresAuth, requirePermission(PermViewGroups), getUserPermissions)
		groups.GET("/:grouptag/members/:userid/benefits", requiresAuth, requirePermission(PermViewGroups), getUserBenefits)
		groups.PUT("/:grouptag/members/:userid/roles/:roleid", requiresAuth, requirePermission(PermManageGroups), assignRole)
		groups.DELETE("/:grouptag/members/:userid/roles/:roleid", requiresAuth, requirePermission(PermManageGroups), removeRole)

		groups.GET("/:grouptag/invites", requiresAuth, requirePermission(PermInviteGroup), getGroupInvites)
		groups.POST("/:grouptag/invites", requiresAuth, requirePermission(PermInviteGroup), sendGroupInvite)
		groups.POST("/:grouptag/invites/:inviteid/accept", requiresAuth, requirePermission(PermJoinGroup), acceptGroupInvite)
		groups.POST("/:grouptag/invites/:inviteid/decline", requiresAuth, requirePermission(PermJoinGroup), declineGroupInvite)
		groups.DELETE("/:grouptag/invites/:inviteid", requiresAuth, requirePermission(PermInviteGroup), revokeGroupInvite)

		groups.GET("/:grouptag/join-requests", requiresAuth, requirePermission(PermInviteGroup), getGroupJoinRequests)
		groups.POST("/:grouptag/join-requests", requiresAuth, requirePermission(PermJoinGroup), requestToJoinGroup)
		groups.POST("/:grouptag/join-requests/:requestid/accept", requiresAuth, requirePermission(PermInviteGroup), acceptGroupJoinRequest)
		groups.POST("/:grouptag/join-requests/:requestid/decline", requiresAuth, requirePermission(PermInviteGroup), declineGroupJoinRequest)

		groups.POST("/:grouptag/transfer/:userid", requiresAuth, requirePermission(PermManageGroups), transferGroupOwnership)
	}

	admin := v2.Group("/admin")
	{
		admin.GET("/tos-update", tosUpdate)
		admin.GET("/users/lookup", getUserBy)
		admin.POST("/users/update", updateUserAdmin)
		admin.POST("/users/delete", deleteUserAdmin)
		admin.POST("/users/ban", banUserAdmin)
		admin.POST("/credits/transfer", transferCreditsAdmin)
		admin.POST("/kofi", handleKofiTransaction)
		admin.POST("/subscriptions", setSubscription)
		admin.POST("/standing", setStandingAdmin)
		admin.POST("/standing/history", getStandingHistoryAdmin)
		admin.POST("/standing/recover", recoverStandingAdmin)
		admin.POST("/moderators", setModeratorAdmin)
	}
}
