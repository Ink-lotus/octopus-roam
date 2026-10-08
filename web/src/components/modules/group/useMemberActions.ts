import { useCallback, useEffect, useId, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { type Group, useReleaseGroupCooldown, useSelectGroupItem, useUpdateGroup } from '@/api/group';
import { ApiError } from '@/api/client';

interface CooldownConfirmation {
    itemId: number;
    version: string;
    until: number;
}

// Shared routing actions; request/round interruption belongs to the caller.
export function useMemberActions(group: Group | undefined, now: number, onSelected?: (cleared: boolean) => Promise<void>) {
    const t = useTranslations('group');
    const select = useSelectGroupItem();
    const release = useReleaseGroupCooldown();
    const manual = useUpdateGroup();
    const confirmationId = useId();
    const [confirmation, setConfirmation] = useState<CooldownConfirmation | null>(null);
    const [pending, setPending] = useState<{ itemId: number; action: 'select' | 'release' } | null>(null);
    const busy = useRef(false);
    const returnFocus = useRef<{ trigger: HTMLElement; container: HTMLElement | null } | null>(null);
    const wasConfirming = useRef(false);

    const confirmedItem = group?.items.find((item) => item.id === confirmation?.itemId);
    if (confirmation && (group?.mode !== 'failover' || !confirmedItem?.available ||
        group.runtime.cooldown_versions[confirmation.itemId] !== confirmation.version ||
        (group.runtime.cooldowns[confirmation.itemId] ?? 0) <= now)) {
        setConfirmation(null);
    }

    useEffect(() => {
        if (wasConfirming.current && !confirmation) {
            const target = returnFocus.current;
            requestAnimationFrame(() => {
                if (document.activeElement !== document.body && document.activeElement !== target?.trigger) return;
                if (target?.trigger.isConnected && !target.trigger.matches(':disabled')) target.trigger.focus();
                else if (target?.container?.isConnected) target.container.focus();
            });
        }
        wasConfirming.current = confirmation !== null;
    }, [confirmation]);

    const keepCooldown = useCallback(() => setConfirmation(null), []);
    const reportError = (error: unknown, action: 'select' | 'release') => {
        toast.error(error instanceof ApiError && error.status === 409
            ? t('route.changed')
            : t(action === 'release' ? 'route.releaseFailed' : 'route.selectFailed'), {
            description: error instanceof Error ? error.message : undefined,
        });
    };

    const activate = async (itemId: number, trigger: HTMLElement) => {
        if (!group || busy.current) return;
        const item = group.items.find((entry) => entry.id === itemId);
        const isCurrent = group.runtime.current_item_id === itemId;
        if (!item || (!item.available && !(group.mode === 'manual' && isCurrent))) {
            toast.error(t('route.unavailable'));
            return;
        }
        // Clicking an open confirmation never becomes a selection, even if the
        // deadline elapsed between pointer-down and this handler.
        if (confirmation?.itemId === itemId) {
            keepCooldown();
            return;
        }
        const until = group.runtime.cooldowns[itemId] ?? 0;
        if (group.mode === 'failover' && until > Date.now()) {
            returnFocus.current = { trigger, container: trigger.closest<HTMLElement>('[data-member-list]') };
            wasConfirming.current = true;
            setConfirmation({ itemId, until, version: group.runtime.cooldown_versions[itemId] });
            return;
        }
        if (group.mode === 'failover' && isCurrent) return;
        setConfirmation(null);
        busy.current = true;
        setPending({ itemId, action: 'select' });
        try {
            if (group.mode === 'failover') {
                await select.mutateAsync({ id: group.id, item_id: itemId, version: group.runtime.selection_version });
            } else {
                await manual.mutateAsync({ id: group.id, active_item_id: isCurrent ? 0 : itemId });
            }
            if (onSelected) await onSelected(group.mode === 'manual' && isCurrent);
            else toast.success(t('toast.updated'));
        } catch (error) {
            reportError(error, 'select');
        } finally {
            busy.current = false;
            setPending(null);
        }
    };

    const releaseCooldown = async () => {
        if (!group || !confirmation || busy.current) return;
        if (confirmation.until <= Date.now()) {
            keepCooldown();
            return;
        }
        busy.current = true;
        setPending({ itemId: confirmation.itemId, action: 'release' });
        try {
            await release.mutateAsync({ id: group.id, item_id: confirmation.itemId, version: confirmation.version });
            keepCooldown();
            toast.success(t('route.released'));
        } catch (error) {
            keepCooldown();
            reportError(error, 'release');
        } finally {
            busy.current = false;
            setPending(null);
        }
    };

    return { activate, releaseCooldown, keepCooldown, confirmation, confirmationId, pending };
}

export type MemberActions = ReturnType<typeof useMemberActions>;
