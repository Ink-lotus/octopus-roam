// 冷却规则配置与运行状态的共享前端类型。
// 这些字段由渠道详情、渠道统计和分组运行态共同使用，避免各页面各自声明一份形状。

export type CooldownScope = 'model' | 'channel';

export interface CooldownRule {
    error_code: string;
    keyword: string;
    cooldown_seconds: number;
    scope: CooldownScope;
}

export interface CooldownHit {
    channel_id: number;
    scope: CooldownScope;
    model_name?: string;
    until: number;
    rule_index: number;
    rule: CooldownRule;
}

export interface ChannelCooldownState {
    channel_id: number;
    version: string;
    rule_cooldowns: CooldownHit[];
}

export const MAX_COOLDOWN_SECONDS = 2147483647;

export function cloneCooldownRules(rules: CooldownRule[] | undefined): CooldownRule[] {
    return (rules ?? []).map((rule) => ({ ...rule }));
}

export function cloneCooldownHits(hits: CooldownHit[] | undefined): CooldownHit[] {
    return (hits ?? []).map((hit) => ({ ...hit, rule: { ...hit.rule } }));
}

export function cooldownHitsForModel(
    hits: CooldownHit[] | undefined,
    channelId: number,
    modelName: string,
): CooldownHit[] {
    return cloneCooldownHits(hits).filter((hit) =>
        hit.channel_id === channelId && (hit.scope === 'channel' || hit.model_name === modelName));
}
