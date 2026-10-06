import { useState } from 'react';
import { Loader2, Logs } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useLogs } from '@/api/log';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { LogCard } from './Item';

type LogFilter = 'all' | 'inProgress' | 'success' | 'failed' | 'canceled';

// Log 展示进程内日志概览，并按 RequestID 实时更新卡片。
export function Log() {
    const t = useTranslations('log');
    const { logs, isLoading, error } = useLogs();
    const [filter, setFilter] = useState<LogFilter>('all');

    if (isLoading) {
        return (
            <div className="flex h-full items-center justify-center">
                <Loader2 className="size-6 animate-spin text-muted-foreground" />
            </div>
        );
    }

    if (logs.length === 0) {
        return (
            <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                {!error && <Logs className="size-8" />}
                <span className="text-sm">{error ? t('list.disconnected') : t('list.empty')}</span>
            </div>
        );
    }

    const visibleLogs = logs.filter((log) => {
        switch (filter) {
            case 'inProgress':
                return log.status === 'running' || log.status === 'committed';
            case 'success':
            case 'failed':
            case 'canceled':
                return log.status === filter;
            default:
                return true;
        }
    });

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            {error && (
                <div className="flex shrink-0 items-center justify-center px-1 pb-3 text-xs text-destructive">
                    <span>{t('list.disconnected')}</span>
                </div>
            )}
            <div className="flex shrink-0 items-center justify-end gap-2 px-1">
                <span className="text-xs text-muted-foreground">{t('filter.label')}</span>
                <Select value={filter} onValueChange={(value) => setFilter(value as LogFilter)}>
                    <SelectTrigger aria-label={t('filter.label')} className="h-9 w-40 rounded-xl">
                        <SelectValue />
                    </SelectTrigger>
                    <SelectContent className="rounded-xl">
                        <SelectItem value="all" className="rounded-lg">{t('filter.all')}</SelectItem>
                        <SelectItem value="inProgress" className="rounded-lg">{t('filter.inProgress')}</SelectItem>
                        <SelectItem value="success" className="rounded-lg">{t('filter.success')}</SelectItem>
                        <SelectItem value="failed" className="rounded-lg">{t('filter.failed')}</SelectItem>
                        <SelectItem value="canceled" className="rounded-lg">{t('filter.canceled')}</SelectItem>
                    </SelectContent>
                </Select>
            </div>
            <div className="min-h-0 flex-1">
                {visibleLogs.length === 0 ? (
                    <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
                        {t('filter.noResults')}
                    </div>
                ) : (
                    <VirtualizedGrid
                        items={visibleLogs}
                        layout="list"
                        columns={{ default: 1 }}
                        estimateItemHeight={104}
                        overscan={8}
                        getItemKey={(log) => `log-${log.id}`}
                        renderItem={(log) => <LogCard log={log} />}
                    />
                )}
            </div>
        </div>
    );
}
