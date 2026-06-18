package main

// Net types for group API responses - these convert UserId fields to Usernames

type GroupTipWithdrawalNet struct {
	Id            string   `json:"id"`
	GroupTag      string   `json:"group_tag"`
	ToUsername    Username `json:"to_username"`
	AmountCredits float64  `json:"amount_credits"`
	CreatedAt     int64    `json:"created_at"`
}

func (w GroupTipWithdrawal) ToNet() GroupTipWithdrawalNet {
	return GroupTipWithdrawalNet{
		Id:            w.Id,
		GroupTag:      w.GroupTag,
		ToUsername:    w.ToUserId.User().GetUsername(),
		AmountCredits: w.AmountCredits,
		CreatedAt:     w.CreatedAt,
	}
}

type GroupInviteNet struct {
	Id           string       `json:"id"`
	GroupTag     string       `json:"group_tag"`
	FromUserId   UserId       `json:"from_user_id"`
	FromUsername Username     `json:"from_username"`
	ToUserId     UserId       `json:"to_user_id"`
	ToUsername   Username     `json:"to_username"`
	Status       InviteStatus `json:"status"`
	CreatedAt    int64        `json:"created_at"`
}

func (i GroupInvite) ToNet() GroupInviteNet {
	return GroupInviteNet{
		Id:           i.Id,
		GroupTag:     i.GroupTag,
		FromUserId:   i.FromUserId,
		FromUsername: i.FromUserId.User().GetUsername(),
		ToUserId:     i.ToUserId,
		ToUsername:   i.ToUserId.User().GetUsername(),
		Status:       i.Status,
		CreatedAt:    i.CreatedAt,
	}
}

type GroupJoinRequestNet struct {
	Id        string        `json:"id"`
	GroupTag  string        `json:"group_tag"`
	UserId    UserId        `json:"user_id"`
	Username  Username      `json:"username"`
	Message   string        `json:"message"`
	Status    RequestStatus `json:"status"`
	CreatedAt int64         `json:"created_at"`
}

func (r GroupJoinRequest) ToNet() GroupJoinRequestNet {
	return GroupJoinRequestNet{
		Id:        r.Id,
		GroupTag:  r.GroupTag,
		UserId:    r.UserId,
		Username:  r.UserId.User().GetUsername(),
		Message:   r.Message,
		Status:    r.Status,
		CreatedAt: r.CreatedAt,
	}
}

type GroupBanNet struct {
	Id           string   `json:"id"`
	GroupTag     string   `json:"group_tag"`
	UserId       UserId   `json:"user_id"`
	Username     Username `json:"username"`
	BannedById   UserId   `json:"banned_by_id"`
	BannedByUser Username `json:"banned_by"`
	Reason       string   `json:"reason"`
	CreatedAt    int64    `json:"created_at"`
}

func (b GroupBan) ToNet() GroupBanNet {
	return GroupBanNet{
		Id:           b.Id,
		GroupTag:     b.GroupTag,
		UserId:       b.UserId,
		Username:     b.UserId.User().GetUsername(),
		BannedById:   b.BannedBy,
		BannedByUser: b.BannedBy.User().GetUsername(),
		Reason:       b.Reason,
		CreatedAt:    b.CreatedAt,
	}
}

type GroupMemberNet struct {
	Id                 string   `json:"id"`
	GroupTag           string   `json:"group_tag"`
	UserId             UserId   `json:"user_id"`
	Username           Username `json:"username"`
	RoleIds            []string `json:"role_ids"`
	JoinedAt           int64    `json:"joined_at"`
	MutedAnnouncements bool     `json:"muted_announcements"`
}

func (m GroupMember) ToNet() GroupMemberNet {
	return GroupMemberNet{
		Id:                 m.Id,
		GroupTag:           m.GroupTag,
		UserId:             m.UserId,
		Username:           m.UserId.User().GetUsername(),
		RoleIds:            m.RoleIds,
		JoinedAt:           m.JoinedAt,
		MutedAnnouncements: m.MutedAnnouncements,
	}
}

type GroupAnnouncementNet struct {
	Id             string   `json:"id"`
	GroupTag       string   `json:"group_tag"`
	Title          string   `json:"title"`
	Body           string   `json:"body"`
	AuthorUsername Username `json:"author_username"`
	CreatedAt      int64    `json:"created_at"`
	PingMembers    bool     `json:"ping_members"`
}

func (a GroupAnnouncement) ToNet() GroupAnnouncementNet {
	return GroupAnnouncementNet{
		Id:             a.Id,
		GroupTag:       a.GroupTag,
		Title:          a.Title,
		Body:           a.Body,
		AuthorUsername: a.AuthorUserId.User().GetUsername(),
		CreatedAt:      a.CreatedAt,
		PingMembers:    a.PingMembers,
	}
}

type GroupEventNet struct {
	Id          string          `json:"id"`
	GroupTag    string          `json:"group_tag"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	StartTime   int64           `json:"start_time"`
	EndTime     int64           `json:"end_time"`
	Location    string          `json:"location"`
	Visibility  EventVisibility `json:"visibility"`
	CreatedBy   Username        `json:"created_by"`
	Published   bool            `json:"published"`
}

func (e GroupEvent) ToNet() GroupEventNet {
	return GroupEventNet{
		Id:          e.Id,
		GroupTag:    e.GroupTag,
		Title:       e.Title,
		Description: e.Description,
		StartTime:   e.StartTime,
		EndTime:     e.EndTime,
		Location:    e.Location,
		Visibility:  e.Visibility,
		CreatedBy:   e.CreatedBy.User().GetUsername(),
		Published:   e.Published,
	}
}

type GroupTipNet struct {
	Id            string   `json:"id"`
	GroupTag      string   `json:"group_tag"`
	FromUsername  Username `json:"from_username"`
	AmountCredits float64  `json:"amount_credits"`
	CreatedAt     int64    `json:"created_at"`
}

func (t GroupTip) ToNet() GroupTipNet {
	return GroupTipNet{
		Id:            t.Id,
		GroupTag:      t.GroupTag,
		FromUsername:  t.FromUserId.User().GetUsername(),
		AmountCredits: t.AmountCredits,
		CreatedAt:     t.CreatedAt,
	}
}
