import { useEffect } from 'react';
import { queryClient } from './client';
import type { ChannelStats } from './channel';
import type { ChannelCooldownState } from './cooldown';
import type { Group, GroupRuntime } from './group';
import { clearPendingGroupRuntimes, removeGroupCache, writeGroupCache, writeGroupRuntime } from './group-cache';
import { invalidateGroupCategories, writeGroupCategories, type GroupCategorySnapshot } from './group-category';

// 分组和渠道页共用一条认证 SSE 连接；引用计数归零后才关闭连接。
let liveEventSource: EventSource | null = null;
let liveEventRefCount = 0;

function compareVersions(left: string, right: string): number {
    if (left === right) return 0;
    const leftParts = left.split(':');
    const rightParts = right.split(':');
    for (let index = 0; index < 2; index++) {
        try {
            const a = BigInt(leftParts[index] ?? '0');
            const b = BigInt(rightParts[index] ?? '0');
            if (a !== b) return a > b ? 1 : -1;
        } catch {
            // 事件版本由服务端生成；异常版本按字符串顺序处理，避免坏事件打断连接。
            const a = leftParts[index] ?? '';
            const b = rightParts[index] ?? '';
            if (a !== b) return a > b ? 1 : -1;
        }
    }
    return 0;
}

function writeChannelCooldown(update: ChannelCooldownState) {
    queryClient.setQueryData<ChannelStats[]>(['channels', 'stats'], (current) => {
        if (!current) return current;
        return current.map((channel) => {
            if (channel.channel_id !== update.channel_id) return channel;
            if (channel.rule_cooldown_version && compareVersions(update.version, channel.rule_cooldown_version) < 0) {
                return channel;
            }
            return {
                ...channel,
                rule_cooldowns: update.rule_cooldowns ?? [],
                rule_cooldown_version: update.version,
            };
        });
    });
}

function invalidateLiveSnapshots() {
    void queryClient.invalidateQueries({ queryKey: ['groups', 'list'] });
    void queryClient.invalidateQueries({ queryKey: ['groups', 'detail'] });
    void queryClient.invalidateQueries({ queryKey: ['channels', 'stats'] });
    void invalidateGroupCategories();
}

function ensureLiveEventSource() {
    if (liveEventSource) return liveEventSource;

    const source = new EventSource('/api/v1/group/events', { withCredentials: true });
    liveEventSource = source;
    source.addEventListener('changed', (event) => {
        writeGroupCache(JSON.parse((event as MessageEvent<string>).data) as Group);
    });
    source.addEventListener('deleted', (event) => {
        removeGroupCache(Number((event as MessageEvent<string>).data));
    });
    source.addEventListener('runtime', (event) => {
        writeGroupRuntime(JSON.parse((event as MessageEvent<string>).data) as GroupRuntime);
    });
    source.addEventListener('channel-cooldown', (event) => {
        writeChannelCooldown(JSON.parse((event as MessageEvent<string>).data) as ChannelCooldownState);
    });
    source.addEventListener('categories', (event) => {
        writeGroupCategories(JSON.parse((event as MessageEvent<string>).data) as GroupCategorySnapshot);
    });
    // SSE 没有事件历史；连接恢复后重新读取所有当前快照，覆盖断线期间的规则到期、删除与清空。
    source.onopen = invalidateLiveSnapshots;
    return source;
}

function releaseLiveEventSource() {
    liveEventSource?.close();
    liveEventSource = null;
    clearPendingGroupRuntimes();
}

export function useLiveEvents(enabled: boolean) {
    useEffect(() => {
        if (!enabled) return;
        liveEventRefCount++;
        ensureLiveEventSource();
        return () => {
            liveEventRefCount--;
            if (liveEventRefCount === 0) releaseLiveEventSource();
        };
    }, [enabled]);
}
