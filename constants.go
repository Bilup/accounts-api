package main

const sessionCookieName = "claw_session"

const LITE_SUBSCRIPTION_KEY = "4f229157f0c40f5a98cbf28efd39cfe8"

var (
	bannedDomains = []string{
		"pornhub.com", "xvideos.com", "xnxx.com", "redtube.com", "youporn.com",
		"xhamster.com", "tube8.com", "spankbang.com", "brazzers.com", "onlyfans.com",
		"chaturbate.com", "livejasmine.com", "cam4.com",
	}

	rateLimits = map[string]RateLimitConfig{
		"default":  {Count: 100, Period: 60},
		"post":     {Count: 5, Period: 60},
		"reply":    {Count: 10, Period: 60},
		"follow":   {Count: 20, Period: 60},
		"profile":  {Count: 30, Period: 60},
		"search":   {Count: 20, Period: 60},
		"ai":       {Count: 5, Period: 10},
		"register": {Count: 5, Period: 10},
		"global":   {Count: 10, Period: 10},

		"auth_default": {Count: 300, Period: 60},
		"auth_post":    {Count: 20, Period: 60},
		"auth_reply":   {Count: 40, Period: 60},
		"auth_follow":  {Count: 60, Period: 60},
		"auth_profile": {Count: 120, Period: 60},
		"auth_search":  {Count: 60, Period: 60},
		"auth_ai":      {Count: 15, Period: 10},
	}
)

func init() {
	groupsData = make(map[string]*GroupData)
}
