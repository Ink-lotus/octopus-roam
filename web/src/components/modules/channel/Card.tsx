import {
    MorphingDialog,
    MorphingDialogTrigger,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
} from '@/components/ui/morphing-dialog';
import { AnimatePresence, motion } from 'motion/react';
import { CheckCircle2, DollarSign, Layers, LoaderCircle, MessageSquare, RefreshCw, XCircle } from 'lucide-react';
import { type ChannelModelRefreshResult, type ChannelStatsFormatted, useEnableChannel, useRefreshChannelModels } from '@/api/channel';
import { usePageActionsStore } from '@/components/common/PageActions';
import { ChannelStats } from './Stats';
import { ChannelForm } from './Form';
import { useTranslations } from 'use-intl';
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip';
import { Switch } from '@/components/ui/switch';
import { Checkbox } from '@/components/ui/checkbox';
import { toast } from 'sonner';
import { useRef, useState } from 'react';

// Card 展示单个渠道的概览; 名称, 启停与统计都在同一条统计查询里, 整份配置只在点开编辑时另取。
export function Card({ channel }: { channel: ChannelStatsFormatted }) {
    const t = useTranslations('channel.card');
    // 布局是渠道页共享的视图选项, 直接取用而不由列表层层传入。
    const layout = usePageActionsStore((state) => state.layouts.channel || 'grid');
    const enableChannel = useEnableChannel();
    const refreshChannelModels = useRefreshChannelModels();
    // 弹窗默认展示统计, 编辑入口在统计视图的头部; 关闭后下次打开仍回到统计, 故点卡片时复位。
    const [openInEditing, setOpenInEditing] = useState(false);
    const [autoAddGroups, setAutoAddGroups] = useState(false);
    const [refreshResult, setRefreshResult] = useState<ChannelModelRefreshResult | null>(null);
    const refreshInFlight = useRef(false);
    const metrics = channel.formatted;

    // 列表布局的统计项, 缺少 unit 时只显示数值
    const listMetrics: { icon: React.ReactNode; label: string; value: string | number; unit?: string }[] = [
        { icon: <MessageSquare className="size-4 shrink-0 text-primary" />, label: t('requestCount'), value: metrics.request_count.formatted.value, unit: metrics.request_count.formatted.unit },
        { icon: <Layers className="size-4 shrink-0 text-primary" />, label: t('model'), value: channel.models.length },
        { icon: <CheckCircle2 className="size-4 shrink-0 text-emerald-500" />, label: t('successRequests'), value: metrics.request_success.formatted.value },
        { icon: <XCircle className="size-4 shrink-0 text-destructive" />, label: t('failedRequests'), value: metrics.request_failed.formatted.value },
        { icon: <DollarSign className="size-4 shrink-0 text-primary" />, label: t('totalCost'), value: metrics.total_cost.formatted.value, unit: metrics.total_cost.formatted.unit },
    ];

    // 网格布局的统计项
    const gridMetrics = [
        { icon: <MessageSquare className="size-4 shrink-0 text-primary" />, label: t('requestCount'), metric: metrics.request_count },
        { icon: <DollarSign className="size-4 shrink-0 text-primary" />, label: t('totalCost'), metric: metrics.total_cost },
    ];

    const handleEnableChange = (checked: boolean) => {
        enableChannel.mutate(
            { id: channel.channel_id, enabled: checked },
            {
                onSuccess: () => {
                    toast.success(checked ? t('toast.enabled') : t('toast.disabled'));
                },
                onError: (error) => {
                    toast.error(error.message);
                },
            }
        );
    };

    const handleRefresh = (event: React.MouseEvent<HTMLButtonElement>) => {
        event.preventDefault();
        event.stopPropagation();
        if (refreshInFlight.current || refreshChannelModels.isPending) return;
        refreshInFlight.current = true;
        setRefreshResult(null);
        refreshChannelModels.mutate(
            { id: channel.channel_id, autoAddGroups },
            {
                onSuccess: (result) => {
                    setRefreshResult(result);
                    if (result.success) {
                        const warnings = [...(result.warnings ?? [])];
                        if (result.error) warnings.push(result.error);
                        const description = [
                            t('refresh.changeSummary', { added: result.added_models.length, removed: result.removed_models.length }),
                            ...warnings.map((warning) => t('refresh.maintenanceWarning', { detail: warning })),
                        ].join('\n');
                        toast.success(
                            result.zero_models
                                ? t('refresh.empty')
                                : t('refresh.success', { count: channel.models.length + result.added_models.length - result.removed_models.length }),
                            { description },
                        );
                    } else {
                        toast.error(t('refresh.failed'), { description: result.error || t('refresh.failedDescription') });
                    }
                },
                onError: (error) => {
                    setRefreshResult({
                        channel_id: channel.channel_id,
                        success: false,
                        error: error.message,
                        zero_models: false,
                        added_models: [],
                        removed_models: [],
                        key_results: [],
                    });
                    toast.error(t('refresh.failed'), { description: error.message });
                },
                onSettled: () => {
                    refreshInFlight.current = false;
                },
            },
        );
    };

    const stopCardTrigger = (event: React.SyntheticEvent) => {
        event.stopPropagation();
    };

    return (
        <MorphingDialog>
            <MorphingDialogTrigger className="w-full">
                <article
                    onClickCapture={() => setOpenInEditing(false)}
                    className="flex flex-col gap-4 rounded-3xl border border-border bg-card text-card-foreground p-4"
                >
                    <header className="flex items-center justify-between gap-2">
                        <Tooltip>
                            <TooltipTrigger asChild>
                                <h3 className="text-lg font-bold truncate min-w-0">{channel.channel_name}</h3>
                            </TooltipTrigger>
                            <TooltipContent key={channel.channel_name} side="top" sideOffset={10} align="center">
                                {channel.channel_name}
                            </TooltipContent>
                        </Tooltip>
                        <div className="flex shrink-0 items-center gap-1" onClick={stopCardTrigger} onPointerDown={stopCardTrigger} onKeyDown={stopCardTrigger}>
                            <label className="flex items-center gap-1 text-xs text-muted-foreground" title={t('refresh.autoAddGroups')}>
                                <Checkbox
                                    checked={autoAddGroups}
                                    onCheckedChange={(checked) => setAutoAddGroups(checked === true)}
                                    aria-label={t('refresh.autoAddGroups')}
                                />
                                <span className="hidden lg:inline">{t('refresh.autoAddGroups')}</span>
                            </label>
                            <button
                                type="button"
                                onClick={handleRefresh}
                                disabled={refreshChannelModels.isPending}
                                aria-label={t('refresh.button')}
                                title={t('refresh.button')}
                                className="inline-flex h-8 items-center gap-1.5 rounded-lg border border-border px-2 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:pointer-events-none disabled:opacity-60"
                            >
                                {refreshChannelModels.isPending ? <LoaderCircle className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
                                <span className="hidden md:inline">{refreshChannelModels.isPending ? t('refresh.pending') : t('refresh.button')}</span>
                            </button>
                            <Switch
                                checked={channel.enabled}
                                onCheckedChange={handleEnableChange}
                                disabled={enableChannel.isPending}
                                onClick={(e) => e.stopPropagation()}
                            />
                        </div>
                    </header>

                    {layout === 'list' ? (
                        <dl className="grid grid-cols-2 gap-2 lg:grid-cols-5">
                            {listMetrics.map(({ icon, label, value, unit }) => (
                                <div key={label} className="rounded-2xl border border-border/70 bg-background/80 p-2">
                                    <dt className="mb-1 flex items-center gap-1.5 text-xs text-muted-foreground">
                                        {icon}
                                        <span className="truncate">{label}</span>
                                    </dt>
                                    <dd className="text-right text-xl font-bold">
                                        {value}
                                        {unit && <span className="ml-1 text-xs font-normal text-muted-foreground">{unit}</span>}
                                    </dd>
                                </div>
                            ))}
                        </dl>
                    ) : (
                        <dl className="grid grid-cols-1 gap-3">
                            {gridMetrics.map(({ icon, label, metric }) => (
                                <div key={label} className="rounded-2xl border border-border/70 bg-background/80 p-2">
                                    <dt className="flex items-center gap-1.5 text-xs text-muted-foreground">
                                        {icon}
                                        <span className="truncate">{label}</span>
                                    </dt>
                                    <dd className="mt-1 text-right text-xl font-bold">
                                        {metric.formatted.value}
                                        <span className="ml-1 text-xs font-normal text-muted-foreground">{metric.formatted.unit}</span>
                                    </dd>
                                </div>
                            ))}
                        </dl>
                    )}

                    {refreshResult && (
                        <div
                            className={`rounded-2xl border p-3 text-xs ${refreshResult.success ? 'border-emerald-500/30 bg-emerald-500/5' : 'border-destructive/30 bg-destructive/5'}`}
                            onClick={stopCardTrigger}
                            onPointerDown={stopCardTrigger}
                        >
                            <div className="flex items-center justify-between gap-2 font-medium">
                                <span>{refreshResult.success ? (refreshResult.zero_models ? t('refresh.empty') : t('refresh.successTitle')) : t('refresh.failed')}</span>
                                {refreshResult.success && (
                                    <span className="text-muted-foreground">
                                        {t('refresh.changeSummary', { added: refreshResult.added_models.length, removed: refreshResult.removed_models.length })}
                                    </span>
                                )}
                            </div>
                            {refreshResult.error && (
                                <p className={`mt-1 ${refreshResult.success ? 'text-amber-700 dark:text-amber-300' : 'text-destructive'}`}>
                                    {refreshResult.success ? t('refresh.warning', { detail: refreshResult.error }) : refreshResult.error}
                                </p>
                            )}
                            {refreshResult.success && refreshResult.warnings?.map((warning, index) => (
                                <p key={`${index}-${warning}`} className="mt-1 text-amber-700 dark:text-amber-300">
                                    {t('refresh.maintenanceWarning', { detail: warning })}
                                </p>
                            ))}
                            <ul className="mt-2 space-y-1 text-muted-foreground">
                                {refreshResult.key_results.map((item) => (
                                    <li key={item.key_name} className="flex min-w-0 flex-col gap-1 sm:flex-row sm:items-start sm:justify-between sm:gap-2">
                                        <span className="min-w-0 break-words sm:max-w-[35%]">{item.key_name}</span>
                                        {item.success ? (
                                            <div className="min-w-0 sm:flex-1 sm:text-right">
                                                <p>{t('refresh.keyModels', { count: item.model_count })}</p>
                                                {item.error && (
                                                    <p className="break-words text-amber-700 dark:text-amber-300" title={item.error}>
                                                        {t('refresh.warning', { detail: item.error })}
                                                    </p>
                                                )}
                                            </div>
                                        ) : (
                                            <span className="min-w-0 break-words text-destructive" title={item.error}>{item.error || t('refresh.keyFailed')}</span>
                                        )}
                                    </li>
                                ))}
                            </ul>
                        </div>
                    )}

                </article>
            </MorphingDialogTrigger>

            <MorphingDialogContainer>
                <MorphingDialogContent
                    dismissOnClickOutside={!openInEditing}
                    className="relative w-full md:max-w-3xl h-fit bg-card text-card-foreground p-4 rounded-3xl overflow-hidden"
                >
                    {/* 高度固定在外层, 与表单自带的高度取同一值: 切换时两者同高, 弹窗才不会随内容缩放。
                        两个视图绝对定位重叠, 退场与入场同时进行: 串行会在两段动画之间留出谁都不在的空档。
                        重叠期间旧视图 pointer-events-none, 否则正在淡出的那份还能挡住点击。
                        位移方向表达前进与后退: 进编辑时表单自右侧推入, 返回统计时反向。 */}
                    <MorphingDialogDescription className="relative h-[min(29rem,calc(100vh-10rem))]">
                        <AnimatePresence initial={false}>
                            {openInEditing ? (
                                <motion.div
                                    key="form"
                                    initial={{ opacity: 0, x: 20 }}
                                    animate={{ opacity: 1, x: 0 }}
                                    exit={{ opacity: 0, x: 20, pointerEvents: 'none' }}
                                    transition={{ type: 'spring', stiffness: 500, damping: 40 }}
                                    className="absolute inset-0"
                                >
                                    <ChannelForm
                                        channelId={channel.channel_id}
                                        onBack={() => setOpenInEditing(false)}
                                    />
                                </motion.div>
                            ) : (
                                <motion.div
                                    key="stats"
                                    initial={{ opacity: 0, x: -20 }}
                                    animate={{ opacity: 1, x: 0 }}
                                    exit={{ opacity: 0, x: -20, pointerEvents: 'none' }}
                                    transition={{ type: 'spring', stiffness: 500, damping: 40 }}
                                    className="absolute inset-0 overflow-y-auto overscroll-contain"
                                >
                                    <ChannelStats
                                        channel={channel}
                                        onEdit={() => setOpenInEditing(true)}
                                    />
                                </motion.div>
                            )}
                        </AnimatePresence>
                    </MorphingDialogDescription>
                </MorphingDialogContent>
            </MorphingDialogContainer>
        </MorphingDialog>
    );
}
