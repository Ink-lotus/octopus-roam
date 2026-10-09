package relay

import (
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
)

func init() {
	op.OnChannelModelsRefreshed(func(id int, before []model.Group) {
		previous := make(map[int]model.Group, len(before))
		for _, group := range before {
			previous[group.ID] = group
		}
		// 先锁住路由再读取最新缓存，避免迟到的刷新回调覆盖已完成的人工编辑通知。
		routeMu.Lock()
		defer routeMu.Unlock()
		for _, group := range op.GroupList() {
			old := previous[group.ID]
			affected := false
			for _, item := range old.Items {
				affected = affected || item.ChannelID == id
			}
			for _, item := range group.Items {
				affected = affected || item.ChannelID == id
			}
			current := routeConfigSignatureOf(group)
			if affected && !sameRouteConfig(routeConfigSignatureOf(old), current) {
				if registered, ok := routeConfigs[group.ID]; ok && sameRouteConfig(registered, current) {
					continue
				}
				if route := routes[group.ID]; route != nil {
					closeRouteWaitersLocked(route)
					delete(routes, group.ID)
				}
				routeConfigs[group.ID] = current
				delete(routeBlocked, group.ID)
			}
		}
	})
}
