import { useEffect, useState } from 'react';
import type { CooldownHit } from '@/api/cooldown';

export function useCooldownClock(hits?: CooldownHit[]) {
    const [now, setNow] = useState(() => Date.now());
    let lastDeadline = 0;
    for (const hit of hits ?? []) lastDeadline = Math.max(lastDeadline, hit.until);

    useEffect(() => {
        if (!lastDeadline) return;
        const tick = () => setNow(Date.now());
        tick();
        if (Date.now() >= lastDeadline) return;
        const timer = window.setInterval(tick, 1000);
        return () => window.clearInterval(timer);
    }, [lastDeadline]);

    return now;
}
