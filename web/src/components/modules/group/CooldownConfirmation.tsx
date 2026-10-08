import { useEffect, useRef } from 'react';
import { KeyRound, Loader2, Lock } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { IconButton } from '@/components/common/IconButton';
import type { MemberActions } from './useMemberActions';

export function CooldownConfirmation({ actions, now }: { actions: MemberActions; now: number }) {
    const t = useTranslations('group');
    const releaseButton = useRef<HTMLButtonElement>(null);
    useEffect(() => { releaseButton.current?.focus(); }, []);
    const confirmation = actions.confirmation;
    if (!confirmation) return null;
    return (
        <div
            id={actions.confirmationId}
            role="group"
            aria-label={t('route.confirmation')}
            aria-describedby={`${actions.confirmationId}-description`}
            className="z-10 col-start-2 row-start-1 flex items-center justify-self-center gap-1"
            onClick={(event) => event.stopPropagation()}
            onKeyDown={(event) => {
                if (event.key !== 'Escape') return;
                event.preventDefault();
                event.stopPropagation();
                actions.keepCooldown();
            }}
        >
            <span id={`${actions.confirmationId}-description`} className="sr-only">
                {t('card.cooling', { seconds: Math.max(0, Math.ceil((confirmation.until - now) / 1000)) })}
                {' '}{t('route.releaseHint')}
            </span>
            <IconButton
                ref={releaseButton}
                disabled={actions.pending !== null}
                aria-label={t(actions.pending?.action === 'release' ? 'route.releasing' : 'route.release')}
                aria-busy={actions.pending?.action === 'release' || undefined}
                tip={t(actions.pending?.action === 'release' ? 'route.releasing' : 'route.release')}
                onClick={(event) => { event.stopPropagation(); void actions.releaseCooldown(); }}
                className="size-6 shrink-0 rounded-md text-primary outline-offset-2 hover:bg-primary/10 hover:text-primary"
            >
                {actions.pending?.action === 'release'
                    ? <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                    : <KeyRound className="size-3.5" aria-hidden="true" />}
            </IconButton>
            <IconButton
                disabled={actions.pending !== null}
                aria-label={t('route.keep')}
                tip={t('route.keep')}
                onClick={(event) => { event.stopPropagation(); actions.keepCooldown(); }}
                className="size-6 shrink-0 rounded-md outline-offset-2 hover:bg-muted"
            >
                <Lock className="size-3.5" aria-hidden="true" />
            </IconButton>
        </div>
    );
}
