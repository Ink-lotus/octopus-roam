import { useState } from 'react';
import { Plus, Trash2, Pencil, Check, RefreshCw } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { IconButton } from '@/components/common/IconButton';
import { useModelProbe } from './probe';
import { grantKey, normalizeAllowedProtocols, type ChannelFormState } from './state';

// maskKey 显示凭据尾部, 其余以点替代。
function maskKey(key: string) {
    return key.length <= 8 ? key : `${key.slice(0, 3)}••••${key.slice(-4)}`;
}

// FormKeys 渠道凭据的增删改。
// 凭据改名后其授权要跟着改名, 否则授权会指向已不存在的凭据; 删除凭据同时删掉它的全部授权。
export function FormKeys({ state, setState }: {
    state: ChannelFormState;
    setState: (next: ChannelFormState) => void;
}) {
    const t = useTranslations('channel.form');
    const { probe, pendingKey } = useModelProbe();
    const [editing, setEditing] = useState<number | null>(null);
    const [draft, setDraft] = useState({ name: '', key: '' });

    const renameGrants = (from: string, to: string) => {
        const grants = new Map(state.grants);
        for (const modelName of state.models) {
            const old = grantKey(modelName, from);
            const grant = grants.get(old);
            if (grant === undefined) continue;
            grants.delete(old);
            grants.set(grantKey(modelName, to), grant);
        }
        return grants;
    };

    const commit = (index: number) => {
        const name = draft.name.trim();
        if (!name || state.keys.some((k, i) => i !== index && k.name === name)) return;
        const previous = state.keys[index].name;
        setState({
            ...state,
            keys: state.keys.map((k, i) => (i === index ? { ...k, name, key: draft.key.trim() } : k)),
            grants: name === previous ? state.grants : renameGrants(previous, name),
        });
        setEditing(null);
    };

    const remove = (index: number) => {
        const removed = state.keys[index].name;
        const grants = new Map(state.grants);
        for (const modelName of state.models) grants.delete(grantKey(modelName, removed));
        setState({ ...state, keys: state.keys.filter((_, i) => i !== index), grants });
    };

    const allowedProtocols = normalizeAllowedProtocols(state.allowed_protocols);
    const grantCount = (keyName: string) =>
        state.models.filter((m) => ((state.grants.get(grantKey(m, keyName)) ?? 0) & allowedProtocols) !== 0).length;

    const add = () => {
        // 名称在渠道内唯一, 递增取一个未占用的默认名。
        let n = state.keys.length + 1;
        while (state.keys.some((k) => k.name === `key-${n}`)) n += 1;
        setState({ ...state, keys: [...state.keys, { name: `key-${n}`, key: '', enabled: true }] });
        setDraft({ name: `key-${n}`, key: '' });
        setEditing(state.keys.length);
    };

    return (
        <div className="min-w-0 space-y-2">
            <div className="flex items-center justify-between shrink-0">
                <span className="text-sm font-medium">{t('keys')} ({state.keys.length})</span>
                <IconButton onClick={add} disabled={editing !== null} className="size-9" tip={t('keyAdd')} aria-label={t('keyAdd')}>
                    <Plus className="size-4" />
                </IconButton>
            </div>

            <div className="space-y-2">
                {state.keys.map((channelKey, index) => editing === index ? (
                    <div key={index} className="flex min-w-0 items-center gap-3 rounded-xl border border-border p-2"
                        onKeyDown={(event) => {
                            if (event.key === 'Enter') {
                                event.preventDefault();
                                commit(index);
                            }
                        }}>
                        <div className="grid min-w-0 flex-1 grid-cols-[minmax(0,1fr)_minmax(0,2fr)] items-center gap-4">
                            <Input
                                value={draft.name}
                                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                                placeholder={t('keyName')}
                                aria-label={t('keyName')}
                                className="rounded-lg h-9 min-w-0"
                            />
                            <Input
                                value={draft.key}
                                onChange={(e) => setDraft({ ...draft, key: e.target.value })}
                                placeholder={t('apiKey')}
                                aria-label={t('apiKey')}
                                className="rounded-lg h-9 min-w-0"
                            />
                        </div>
                        <IconButton
                            onClick={() => commit(index)}
                            disabled={!draft.name.trim() || state.keys.some((key, i) => i !== index && key.name === draft.name.trim())}
                            className="size-9 shrink-0"
                            tip={t('save')}
                            aria-label={t('save')}
                        >
                            <Check className="size-4" />
                        </IconButton>
                    </div>
                ) : (
                    <div key={index} className="flex flex-wrap items-center gap-2 rounded-xl border border-border px-3 py-2">
                        <div className="grid min-w-0 flex-1 basis-40 grid-cols-[minmax(0,1fr)_minmax(0,2fr)] items-center gap-4">
                            <span className="min-w-0 truncate text-sm font-medium" title={channelKey.name}>{channelKey.name}</span>
                            <span className="min-w-0 truncate text-xs font-mono text-muted-foreground">
                                {channelKey.key ? maskKey(channelKey.key) : t('keyEmpty')}
                            </span>
                        </div>
                        <div className="ml-auto flex shrink-0 items-center gap-1">
                        {/* 只显示该凭据自己的授权数, 模型集合是渠道级共享的, 总数对每条凭据都一样, 摆出来会被误读成它已获取的模型数。 */}
                        <span className="text-xs text-muted-foreground tabular-nums">
                            {grantCount(channelKey.name)}
                        </span>
                        <Switch
                            checked={channelKey.enabled}
                            aria-label={`${channelKey.name} ${t('enabled')}`}
                            onCheckedChange={(checked) => setState({
                                ...state,
                                keys: state.keys.map((k, i) => (i === index ? { ...k, enabled: checked } : k)),
                            })}
                        />
                        {/* 逐条凭据探测: 各凭据在上游被授权的模型不同, 需按凭据分别取回。 */}
                        <IconButton
                            onClick={() => probe(state, setState, channelKey.name)}
                            disabled={pendingKey !== null || !channelKey.key.trim() || !state.base_url.trim()}
                            className="size-8"
                            tip={t('modelRefresh')}
                            aria-label={t('modelRefresh')}
                        >
                            <RefreshCw className={`size-4 ${pendingKey === channelKey.name ? 'animate-spin' : ''}`} />
                        </IconButton>
                        <IconButton
                            onClick={() => { setDraft({ name: channelKey.name, key: channelKey.key }); setEditing(index); }}
                            className="size-8"
                            tip={t('edit')}
                            aria-label={t('edit')}
                            disabled={editing !== null}
                        >
                            <Pencil className="size-4" />
                        </IconButton>
                        <IconButton
                            onClick={() => remove(index)}
                            className="size-8 hover:text-destructive"
                            tip={t('delete')}
                            aria-label={t('delete')}
                            disabled={editing !== null}
                        >
                            <Trash2 className="size-4" />
                        </IconButton>
                        </div>
                    </div>
                ))}
            </div>
        </div>
    );
}
