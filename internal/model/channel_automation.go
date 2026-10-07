package model

import "time"

func (c ChannelConfig) FollowsGlobalAutoUpdate() bool {
	return c.FollowGlobalAutoUpdate == nil || *c.FollowGlobalAutoUpdate
}

// ChannelRefreshRecord 仅保存最近完成的诊断，不作为备份恢复配置。
type ChannelRefreshRecord struct {
	ChannelID   int                       `json:"-" gorm:"primaryKey;autoIncrement:false"`
	StartedAt   time.Time                 `json:"started_at"`
	FinishedAt  time.Time                 `json:"finished_at"`
	Source      string                    `json:"source"`
	Status      string                    `json:"status"`
	BeforeCount int                       `json:"before_count"`
	AfterCount  int                       `json:"after_count"`
	Result      ChannelModelRefreshResult `json:"result" gorm:"serializer:json"`
}

type ChannelRefreshStatus struct {
	Running   bool                  `json:"running"`
	StartedAt *time.Time            `json:"started_at,omitempty"`
	Last      *ChannelRefreshRecord `json:"last,omitempty"`
}

// 列表只携带固定大小摘要，逐 Key 结果和模型名单按渠道单取。
type ChannelRefreshSummary struct {
	Running   bool                         `json:"running"`
	StartedAt *time.Time                   `json:"started_at,omitempty"`
	Last      *ChannelRefreshSummaryRecord `json:"last,omitempty"`
}

type ChannelRefreshSummaryRecord struct {
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	Source       string    `json:"source"`
	Status       string    `json:"status"`
	BeforeCount  int       `json:"before_count"`
	AfterCount   int       `json:"after_count"`
	AddedCount   int       `json:"added_count"`
	RemovedCount int       `json:"removed_count"`
	WarningCount int       `json:"warning_count"`
}

type ChannelConcurrencyStatus struct {
	Active  int `json:"active"`
	Waiting int `json:"waiting"`
}
