import { useState } from 'react';
import { ChevronLeft, Plus, Trash2 } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import {
    type ChannelDetail,
    Protocol,
    useChannelDetail,
    useCreateChannel,
    useUpdateChannel,
} from '@/api/channel';
import { CHANNEL_PRESETS, type ChannelPreset } from '@/lib/channel-presets';
import { useMorphingDialog } from '@/components/ui/morphing-dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { useChannelPreferences } from '@/stores/channel';
import { FormGrants } from './FormGrants';
import { FormKeys } from './FormKeys';
import { IconButton } from '@/components/common/IconButton';
import { RegexFilterField } from '@/components/common/RegexFilterField';
import { isValidRegex } from '@/lib/regex';
import { MAX_COOLDOWN_SECONDS, type CooldownRule } from '@/api/cooldown';
import {
    channelNameFromURL,
    emptyFormState,
    fromChannel,
    normalizeAllowedProtocols,
    toChannelDetail,
    type ChannelFormState,
} from './state';

type StepID = 'preset' | 'connection' | 'grants' | 'advanced';

type CooldownRuleError = 'condition' | 'seconds' | 'scope' | null;

function cooldownRuleError(rule: CooldownRule): CooldownRuleError {
    if (!rule.error_code.trim() && !rule.keyword.trim()) return 'condition';
    if (!Number.isSafeInteger(rule.cooldown_seconds)
        || rule.cooldown_seconds < 1
        || rule.cooldown_seconds > MAX_COOLDOWN_SECONDS) return 'seconds';
    if (rule.scope !== 'model' && rule.scope !== 'channel') return 'scope';
    return null;
}

// ChannelForm 新建与编辑共用; 编辑时按 id 取回整份配置, 到齐后再进表单。
// 配置不随渠道列表下发, 故编辑必然要等这一趟请求; 占位与表单同高, 弹窗不会因此跳动。
export function ChannelForm({ channelId, onBack }: {
    channelId?: number;
    onBack?: () => void; // 编辑态由统计视图切入, 给出返回入口; 新建态没有可返回的视图。
}) {
    if (channelId === undefined) {
        return <ChannelFormFields />;
    }
    return <ChannelEditForm key={channelId} channelId={channelId} onBack={onBack} />;
}

function ChannelEditForm({ channelId, onBack }: { channelId: number; onBack?: () => void }) {
    const t = useTranslations('channel.form');
    const { data: detail, isFetchedAfterMount, isError } = useChannelDetail(channelId);
    const [initialDetail, setInitialDetail] = useState<ChannelDetail>();
    // 新编辑必须等本次读取成功；之后的后台刷新不重置用户草稿。
    if (!initialDetail && detail && isFetchedAfterMount && !isError) {
        setInitialDetail(detail);
    }
    if (!initialDetail) {
        return (
            <div className="flex items-center justify-center h-[min(29rem,calc(100vh-10rem))]">
                <p role={isError ? 'alert' : 'status'} className="text-sm text-muted-foreground">
                    {isError ? t('detailFailed') : t('detailLoading')}
                </p>
            </div>
        );
    }
    return <ChannelFormFields channel={initialDetail} onBack={onBack} />;
}

// ChannelFormFields 承载表单本体; 初始状态在挂载时定稿, 故必须等配置到齐后才渲染。
// 新建从模板步骤起, 编辑跳过模板直接进连接步骤; 步骤顺序即数据依赖顺序:
// 模板给出地址与路径 -> 拉取需要地址与凭据 -> 授权引用凭据与模型。
function ChannelFormFields({ channel, onBack }: { channel?: ChannelDetail; onBack?: () => void }) {
    const t = useTranslations('channel.form');
    const tAuto = useTranslations('automation');
    const tRefresh = useTranslations('channel.card.refresh');
    const { setIsOpen } = useMorphingDialog();
    const createChannel = useCreateChannel();
    const updateChannel = useUpdateChannel();
    const savedAutoAddGroups = useChannelPreferences((preferences) => channel ? preferences.autoAddGroups[channel.id] ?? true : true);
    const saveAutoAddGroups = useChannelPreferences((preferences) => preferences.setAutoAddGroups);
    const [autoAddGroups, setAutoAddGroups] = useState(savedAutoAddGroups);
    const [state, setState] = useState<ChannelFormState>(channel ? fromChannel(channel) : emptyFormState);
    const [automaticName, setAutomaticName] = useState(!channel?.name.trim()
        || channel.name === channelNameFromURL(channel.base_url));
    const [step, setStep] = useState<StepID>(channel ? 'connection' : 'preset');
    const isPending = createChannel.isPending || updateChannel.isPending;
    // 新建与编辑弹窗可同时存在, 控件 id 需按渠道隔离。
    const idPrefix = channel ? `channel-${channel.id}` : 'new-channel';

    const name = state.name.trim() || channelNameFromURL(state.base_url);
    // 凭据可先保存为空白条目，之后再填写 Key、获取模型；名称仍需有效。
    const canSubmit = name !== ''
        && state.base_url.trim() !== ''
        && [state.openai_chat_completion_path, state.openai_response_path, state.anthropic_message_path]
            .every((path) => path === '' || path.startsWith('/'))
        && state.keys.length > 0
        && state.keys.every((k) => k.name.trim() !== '')
        && Number.isSafeInteger(state.max_concurrency) && state.max_concurrency >= 0
        && state.cooldown_rules.every((rule) => cooldownRuleError(rule) === null);

    const steps: { id: StepID; label: string }[] = [
        ...(channel ? [] : [{ id: 'preset' as StepID, label: t('stepPreset') }]),
        { id: 'connection', label: t('stepConnection') },
        { id: 'grants', label: t('stepGrants') },
        { id: 'advanced', label: t('stepAdvanced') },
    ];

    const applyPreset = (preset: ChannelPreset) => {
        setState({
            ...state,
            name: automaticName ? channelNameFromURL(preset.base_url) : state.name,
            dialect: preset.dialect,
            base_url: preset.base_url,
            openai_chat_completion_path: preset.openai_chat_completion_path,
            openai_response_path: preset.openai_response_path,
            anthropic_message_path: preset.anthropic_message_path,
        });
        setStep('connection');
    };

    // 新建与编辑都一趟完成且都提交整份配置: 授权按名称引用, 与凭据和模型在同一次请求里原子生效。
    const submit = (event: React.FormEvent<HTMLFormElement>) => {
        event.preventDefault();
        if (!canSubmit) return;
        if (!isValidRegex(state.match_regex.trim())) {
            setStep('advanced');
            return;
        }
        const detail = toChannelDetail({ ...state, name }, channel?.id ?? 0);
        const mutation = channel ? updateChannel : createChannel;
        mutation.mutate({ ...detail, auto_add_groups: autoAddGroups }, {
            onSuccess: (saved) => {
                saveAutoAddGroups(saved.id, autoAddGroups);
                setIsOpen(false);
            },
            onError: (error) => toast.error(t('saveFailed'), { description: error.message }),
        });
    };

    // 表单高度固定, 否则切换步骤时弹窗会随内容高度跳动; 内容更高的步骤由步骤区内部滚动消化。
    // 连接页合并凭据后由步骤区内部滚动，底部保存按钮始终可见。
    // calc 一项夹住矮屏, 弹窗不提供滚动, 内容超出视口时底部按钮会点不到。详情视图取同一高度以对齐尺寸。
    return (
        <form onSubmit={submit} className="flex flex-col md:flex-row gap-6 h-[min(29rem,calc(100vh-10rem))]">
            <nav className="md:w-28 shrink-0 flex md:flex-col gap-1 overflow-x-auto pt-1">
                {steps.map((s) => (
                    <button
                        key={s.id}
                        type="button"
                        onClick={() => setStep(s.id)}
                        className={`rounded-xl px-3 py-2 text-left text-sm font-medium whitespace-nowrap transition-colors ${step === s.id ? 'bg-muted' : 'text-muted-foreground hover:bg-muted/50'}`}
                    >
                        {s.label}
                    </button>
                ))}
                {/* 返回统计的入口与步骤同列, 顶到底部与步骤拉开距离, 表明它不是其中一步。
                    窄屏下步骤横向排布, 此时它跟在末尾, mt-auto 不生效。 */}
                {onBack && (
                    <button
                        type="button"
                        onClick={onBack}
                        className="md:mt-auto flex items-center gap-1.5 rounded-xl px-3 py-2 text-left text-sm whitespace-nowrap text-muted-foreground transition-colors hover:bg-muted/50"
                    >
                        <ChevronLeft className="size-4 shrink-0" />
                        {t('backToStats')}
                    </button>
                )}
            </nav>

            <div className="flex-1 min-w-0 flex flex-col min-h-0">
                <div className="flex-1 min-h-0 overflow-y-auto overscroll-contain p-1">
                    {step === 'preset' && (
                        <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                            {CHANNEL_PRESETS.map((preset) => (
                                <button
                                    key={preset.id}
                                    type="button"
                                    onClick={() => applyPreset(preset)}
                                    className="flex items-center gap-4 rounded-xl border border-border px-5 py-4 hover:bg-muted/50 transition-colors"
                                >
                                    <preset.Icon className={`size-7 shrink-0 ${preset.iconClassName ?? ''}`} />
                                    <span className="text-base font-medium truncate">{preset.label}</span>
                                </button>
                            ))}
                        </div>
                    )}

                    {step === 'connection' && (
                        <div className="space-y-4 pt-2">
                            <div className="space-y-2">
                                <Label htmlFor={`${idPrefix}-name`}>{t('name')}</Label>
                                <Input
                                    id={`${idPrefix}-name`}
                                    value={state.name || (automaticName ? name : '')}
                                    onChange={(e) => {
                                        setAutomaticName(e.target.value.trim() === '');
                                        setState({ ...state, name: e.target.value });
                                    }}
                                    className="rounded-xl"
                                    required
                                />
                            </div>
                            <div className="space-y-2">
                                <Label htmlFor={`${idPrefix}-base-url`}>{t('baseUrl')}</Label>
                                <Input
                                    id={`${idPrefix}-base-url`}
                                    type="url"
                                    value={state.base_url}
                                    onChange={(e) => setState({
                                        ...state,
                                        base_url: e.target.value,
                                        name: automaticName ? channelNameFromURL(e.target.value) : state.name,
                                    })}
                                    className="rounded-xl"
                                    required
                                />
                            </div>
                            <FormKeys state={state} setState={setState} />
                            {([
                                ['openai_chat_completion_path', 'OpenAI Chat', Protocol.OpenAIChatCompletion],
                                ['openai_response_path', 'OpenAI Responses', Protocol.OpenAIResponse],
                                ['anthropic_message_path', 'Anthropic', Protocol.AnthropicMessage],
                            ] as const).map(([field, label, bit]) => (
                                <div key={field} className="space-y-2">
                                    <div className="flex items-center justify-between gap-2">
                                        <Label htmlFor={`${idPrefix}-${field}`}>{label}</Label>
                                        <Switch
                                            aria-label={label}
                                            title={t('allowedProtocolsHint')}
                                            checked={(state.allowed_protocols & bit) !== 0}
                                            disabled={state.allowed_protocols === bit}
                                            onCheckedChange={(checked) => setState({
                                                ...state,
                                                allowed_protocols: normalizeAllowedProtocols(
                                                    checked ? state.allowed_protocols | bit : state.allowed_protocols & ~bit,
                                                ),
                                            })}
                                        />
                                    </div>
                                    <Input
                                        id={`${idPrefix}-${field}`}
                                        value={state[field]}
                                        onChange={(e) => setState({ ...state, [field]: e.target.value })}
                                        aria-invalid={state[field] !== '' && !state[field].startsWith('/')}
                                        className="rounded-xl font-mono text-sm"
                                    />
                                </div>
                            ))}
                            <div className="flex flex-wrap items-center gap-6">
                                {([['enabled', t('enabled')], ['proxy', t('proxy')]] as const).map(([field, label]) => (
                                    <label key={field} className="flex items-center gap-2 cursor-pointer">
                                        <Switch
                                            checked={state[field]}
                                            onCheckedChange={(checked) => setState({ ...state, [field]: checked })}
                                        />
                                        <span className="text-sm">{label}</span>
                                    </label>
                                ))}
                            </div>
                        </div>
                    )}

                    {step === 'grants' && <FormGrants state={state} setState={setState} />}

                    {step === 'advanced' && (
                        <div className="space-y-4 pt-2">
                            <div className="flex items-center justify-between gap-3">
                                <Label htmlFor={`${idPrefix}-auto-add-groups`}>{tRefresh('autoAddGroups')}</Label>
                                <Switch id={`${idPrefix}-auto-add-groups`} checked={autoAddGroups}
                                    onCheckedChange={setAutoAddGroups} />
                            </div>
                            <div className="space-y-2">
                                <div className="flex items-center justify-between gap-3">
                                    <Label htmlFor={`${idPrefix}-follow`}>{tAuto('follow')}</Label>
                                    <Switch id={`${idPrefix}-follow`} checked={state.follow_global_auto_update}
                                        onCheckedChange={(checked) => setState({ ...state, follow_global_auto_update: checked })}
                                        aria-describedby={`${idPrefix}-follow-hint`} />
                                </div>
                                <p id={`${idPrefix}-follow-hint`} className="text-xs text-muted-foreground">{tAuto('followHint')}</p>
                            </div>
                            <div className="space-y-2">
                                <Label htmlFor={`${idPrefix}-concurrency`}>{tAuto('maxConcurrency')}</Label>
                                <Input id={`${idPrefix}-concurrency`} type="number" min={0} step={1}
                                    value={Number.isNaN(state.max_concurrency) ? '' : state.max_concurrency}
                                    onChange={(event) => setState({ ...state, max_concurrency: event.target.valueAsNumber })}
                                    aria-describedby={`${idPrefix}-concurrency-hint`} className="rounded-xl" />
                                <p id={`${idPrefix}-concurrency-hint`} className="text-xs text-muted-foreground">{tAuto('concurrencyHint')}</p>
                            </div>
                            <div className="space-y-3 rounded-2xl border border-border/70 bg-background/50 p-3">
                                <div className="flex items-start justify-between gap-3">
                                    <div className="space-y-1">
                                        <Label>{t('cooldownRules')}</Label>
                                        <p className="text-xs text-muted-foreground">{t('cooldownRulesHint')}</p>
                                    </div>
                                    <IconButton
                                        type="button"
                                        aria-label={t('cooldownRuleAdd')}
                                        tip={t('cooldownRuleAdd')}
                                        onClick={() => setState({
                                            ...state,
                                            cooldown_rules: [...state.cooldown_rules, {
                                                error_code: '',
                                                keyword: '',
                                                cooldown_seconds: Number.NaN,
                                                scope: 'model',
                                            }],
                                        })}
                                        className="size-9 shrink-0"
                                    >
                                        <Plus className="size-4" />
                                    </IconButton>
                                </div>
                                {state.cooldown_rules.length === 0 ? (
                                    <p className="rounded-xl border border-dashed border-border px-3 py-2 text-xs text-muted-foreground">
                                        {t('cooldownRulesEmpty')}
                                    </p>
                                ) : (
                                    <div className="space-y-3">
                                        {state.cooldown_rules.map((rule, index) => {
                                            const error = cooldownRuleError(rule);
                                            const errorId = `${idPrefix}-cooldown-${index}-error`;
                                            const updateRule = (patch: Partial<CooldownRule>) => setState({
                                                ...state,
                                                cooldown_rules: state.cooldown_rules.map((item, itemIndex) =>
                                                    itemIndex === index ? { ...item, ...patch } : item),
                                            });
                                            return (
                                                <fieldset key={index} className="space-y-2 rounded-xl border border-border/60 p-3">
                                                    <legend className="px-1 text-xs font-medium text-muted-foreground">
                                                        {t('cooldownRule', { index: index + 1 })}
                                                    </legend>
                                                    <div className="grid gap-2 sm:grid-cols-2">
                                                        <div className="space-y-1">
                                                            <Label htmlFor={`${idPrefix}-cooldown-${index}-code`}>{t('cooldownErrorCode')}</Label>
                                                            <Input
                                                                id={`${idPrefix}-cooldown-${index}-code`}
                                                                value={rule.error_code}
                                                                onChange={(event) => updateRule({ error_code: event.target.value })}
                                                                aria-invalid={error === 'condition'}
                                                                aria-describedby={error === 'condition' ? errorId : undefined}
                                                                className="rounded-xl"
                                                            />
                                                        </div>
                                                        <div className="space-y-1">
                                                            <Label htmlFor={`${idPrefix}-cooldown-${index}-keyword`}>{t('cooldownKeyword')}</Label>
                                                            <Input
                                                                id={`${idPrefix}-cooldown-${index}-keyword`}
                                                                value={rule.keyword}
                                                                onChange={(event) => updateRule({ keyword: event.target.value })}
                                                                aria-invalid={error === 'condition'}
                                                                aria-describedby={error === 'condition' ? errorId : undefined}
                                                                className="rounded-xl"
                                                            />
                                                        </div>
                                                        <div className="space-y-1">
                                                            <Label htmlFor={`${idPrefix}-cooldown-${index}-seconds`}>{t('cooldownSeconds')}</Label>
                                                            <Input
                                                                id={`${idPrefix}-cooldown-${index}-seconds`}
                                                                type="number"
                                                                min={1}
                                                                max={MAX_COOLDOWN_SECONDS}
                                                                step={1}
                                                                value={Number.isNaN(rule.cooldown_seconds) ? '' : rule.cooldown_seconds}
                                                                onChange={(event) => updateRule({ cooldown_seconds: event.target.valueAsNumber })}
                                                                aria-invalid={error === 'seconds'}
                                                                aria-describedby={error === 'seconds' ? errorId : undefined}
                                                                className="rounded-xl"
                                                            />
                                                        </div>
                                                        <div className="space-y-1">
                                                            <Label htmlFor={`${idPrefix}-cooldown-${index}-scope`}>{t('cooldownScope')}</Label>
                                                            <select
                                                                id={`${idPrefix}-cooldown-${index}-scope`}
                                                                value={rule.scope}
                                                                onChange={(event) => updateRule({ scope: event.target.value as CooldownRule['scope'] })}
                                                                aria-invalid={error === 'scope'}
                                                                aria-describedby={error === 'scope' ? errorId : undefined}
                                                                className="border-input h-9 w-full rounded-md border bg-transparent px-3 py-1 text-base outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] md:text-sm"
                                                            >
                                                                <option value="model">{t('cooldownScopeModel')}</option>
                                                                <option value="channel">{t('cooldownScopeChannel')}</option>
                                                            </select>
                                                        </div>
                                                    </div>
                                                    <div className="flex items-start justify-between gap-2">
                                                        <p id={errorId} role={error ? 'alert' : undefined} className="text-xs text-destructive">
                                                            {error === 'condition' && t('cooldownRuleConditionError')}
                                                            {error === 'seconds' && t('cooldownRuleSecondsError')}
                                                            {error === 'scope' && t('cooldownRuleScopeError')}
                                                        </p>
                                                        <IconButton
                                                            type="button"
                                                            aria-label={t('cooldownRuleRemove')}
                                                            tip={t('cooldownRuleRemove')}
                                                            onClick={() => setState({
                                                                ...state,
                                                                cooldown_rules: state.cooldown_rules.filter((_, itemIndex) => itemIndex !== index),
                                                            })}
                                                            className="size-8 shrink-0 hover:text-destructive"
                                                        >
                                                            <Trash2 className="size-4" />
                                                        </IconButton>
                                                    </div>
                                                </fieldset>
                                            );
                                        })}
                                    </div>
                                )}
                            </div>
                            {([
                                ['channel_proxy', t('channelProxy')],
                            ] as const).map(([field, label]) => (
                                <div key={field} className="space-y-2">
                                    <Label htmlFor={`${idPrefix}-${field}`}>{label}</Label>
                                    <Input
                                        id={`${idPrefix}-${field}`}
                                        value={state[field]}
                                        onChange={(e) => setState({ ...state, [field]: e.target.value })}
                                        className="rounded-xl"
                                    />
                                </div>
                            ))}
                            <RegexFilterField
                                id={`${idPrefix}-match_regex`}
                                label={t('matchRegex')}
                                value={state.match_regex}
                                validationValue={state.match_regex.trim()}
                                onChange={(value) => setState({ ...state, match_regex: value })}
                            />
                            <div className="space-y-2">
                                <Label htmlFor={`${idPrefix}-param-override`}>{t('paramOverride')}</Label>
                                <textarea
                                    id={`${idPrefix}-param-override`}
                                    value={state.param_override}
                                    onChange={(e) => setState({ ...state, param_override: e.target.value })}
                                    className="min-h-24 w-full rounded-xl border border-border bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                                />
                            </div>

                            <div className="space-y-2">
                                <div className="flex items-center justify-between">
                                    <Label>{t('customHeader')}</Label>
                                    <IconButton
                                        onClick={() => setState({
                                            ...state,
                                            custom_header: [...state.custom_header, { header_key: '', header_value: '' }],
                                        })}
                                        className="size-9"
                                        tip={t('customHeaderAdd')}
                                    >
                                        <Plus className="size-4" />
                                    </IconButton>
                                </div>
                                {/* 列名只在有行时出现, 两列各占一半, 与下方输入框对齐; 末尾留出删除按钮的宽度。 */}
                                {state.custom_header.length > 0 && (
                                    <div className="flex items-center gap-2">
                                        <Label className="flex-1 text-muted-foreground">{t('customHeaderKey')}</Label>
                                        <Label className="flex-1 text-muted-foreground">{t('customHeaderValue')}</Label>
                                        <span className="size-9 shrink-0" />
                                    </div>
                                )}
                                {state.custom_header.map((header, idx) => (
                                    <div key={idx} className="flex items-center gap-2">
                                        {(['header_key', 'header_value'] as const).map((field) => (
                                            <Input
                                                key={field}
                                                value={header[field]}
                                                onChange={(e) => setState({
                                                    ...state,
                                                    custom_header: state.custom_header.map((h, i) =>
                                                        i === idx ? { ...h, [field]: e.target.value } : h),
                                                })}
                                                className="rounded-xl flex-1"
                                            />
                                        ))}
                                        <IconButton
                                            onClick={() => setState({
                                                ...state,
                                                custom_header: state.custom_header.filter((_, i) => i !== idx),
                                            })}
                                            className="size-9 hover:text-destructive"
                                            tip={t('delete')}
                                        >
                                            <Trash2 className="size-4" />
                                        </IconButton>
                                    </div>
                                ))}
                            </div>
                        </div>
                    )}
                </div>

                <div className="shrink-0 flex justify-end gap-2 pt-4">
                    <Button
                        type="button"
                        variant="secondary"
                        onClick={() => setIsOpen(false)}
                        className="rounded-xl h-9 px-4"
                    >
                        {t('cancel')}
                    </Button>
                    <Button type="submit" disabled={isPending || !canSubmit} className="rounded-xl h-9 px-4">
                        {channel ? t('save') : t('submit')}
                    </Button>
                </div>
            </div>
        </form>
    );
}
