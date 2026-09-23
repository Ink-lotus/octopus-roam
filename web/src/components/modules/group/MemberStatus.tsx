import { useEffect, useState } from 'react';
import { CircleCheck } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';
import type { Group } from '@/api/group';

// MemberStatusProps 描述成员的冷却和当前使用状态。
interface MemberStatusProps {
    group: Group; // group 提供当前路由和成员冷却时间戳。
    itemId?: number; // itemId 是待展示状态的成员 ID。
    now: number; // now 是所属列表共享的当前 Unix 毫秒时间。
    active?: boolean; // active 表示该成员当前正在使用。
    activeClassName?: string; // activeClassName 调整原有选中图标在不同列表中的间距。
}

// useRuntimeClock 为一个或多个分组提供共享倒计时当前时间。
export function useRuntimeClock(source?: Group | Group[]) {
    const [now, setNow] = useState(() => Date.now());
    const groups = source === undefined ? [] : Array.isArray(source) ? source : [source];
    let enabled = false;
    let lastDeadline = 0;
    for (const group of groups) {
        if (group.mode !== 'failover') continue;
        enabled = true;
        for (const cooldownUntil of Object.values(group.runtime.cooldowns)) {
            lastDeadline = Math.max(lastDeadline, cooldownUntil);
        }
    }

    useEffect(() => {
        if (!enabled) return;

        let timer = 0;
        const tick = () => {
            const next = Date.now();
            setNow(next);
            if (next >= lastDeadline) window.clearInterval(timer);
        };
        // 依赖变化后先异步校正一次，避免上一轮倒计时结束后残留的旧时间参与判断。
        const immediate = window.setTimeout(tick, 0);
        if (Date.now() < lastDeadline) timer = window.setInterval(tick, 1000);

        return () => {
            window.clearTimeout(immediate);
            window.clearInterval(timer);
        };
    }, [enabled, lastDeadline]);

    return now;
}

// MemberStatus 展示成员的冷却倒计时或当前使用状态。
export function MemberStatus({ group, itemId, now, active = false, activeClassName }: MemberStatusProps) {
    const t = useTranslations('group.card');

    if (group.mode === 'failover' && itemId !== undefined) {
        const cooldownUntil = group.runtime.cooldowns[itemId] ?? 0;
        if (now < cooldownUntil) {
            return (
                <Badge
                    variant="outline"
                    className="shrink-0 border-orange-500/30 bg-orange-500/10 px-1.5 py-0 text-[10px] font-medium text-orange-600 dark:text-orange-400"
                >
                    {t('cooling', { seconds: Math.ceil((cooldownUntil - now) / 1000) })}
                </Badge>
            );
        }

        if (group.runtime.current_item_id === itemId) {
            return (
                <Badge variant="outline" className="shrink-0 gap-1 border-primary/30 bg-primary/5 px-1.5 py-0 text-[10px] font-medium text-primary">
                    <CircleCheck className="size-3" aria-hidden="true" />
                    {t('normal')}
                </Badge>
            );
        }
    }

    return active ? (
        <span aria-hidden="true" className={cn('inline-flex shrink-0 text-primary', activeClassName)}>
            <CircleCheck className="size-4" />
        </span>
    ) : null;
}
