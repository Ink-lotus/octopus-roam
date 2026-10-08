import { useIsMutating, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, apiRequest } from './client';

export type RuleKind = 'field_name' | 'literal' | 'regex';
export type PreviewInputType = 'text' | 'json' | 'url' | 'headers';
export interface PrivacyRuleForm {
    name: string;
    kind: RuleKind;
    pattern: string;
    enabled: boolean;
}
export interface PrivacyRule extends PrivacyRuleForm { id: string }
export interface PrivacyBuiltin {
    id: string;
    key: string;
    enabled: boolean;
    input_type: PreviewInputType;
    example: string;
    known_values?: string[];
}
export interface PrivacyRules {
    revision: string;
    enabled: boolean;
    builtins: PrivacyBuiltin[];
    custom_rules: PrivacyRule[];
    limits: {
        custom_rules: number;
        name_runes: number;
        pattern_bytes: number;
        config_bytes: number;
        preview_bytes: number;
        known_values: number;
        request_bytes: number;
    };
}
export type PrivacyMutation = { revision: string } & (
    | { action: 'create'; rule: PrivacyRuleForm }
    | { action: 'update'; id: string; rule: PrivacyRuleForm }
    | { action: 'toggle'; id: string; enabled: boolean }
    | { action: 'delete'; id: string }
);
export interface PrivacyPreviewRequest {
    revision: string;
    input_type: PreviewInputType;
    input: string;
    mode: 'current' | 'enabled';
    rule_id?: string;
    draft?: PrivacyRuleForm;
    known_values?: string[];
}
export interface PrivacyPreviewResult {
    revision: string;
    output: string;
    hits: { rule_id: string; count: number }[];
}
export interface PrivacyValidation { field: string; code: string; message: string }

export function privacyValidation(error: unknown): PrivacyValidation | undefined {
    if (error instanceof ApiError && error.details && typeof error.details === 'object') {
        const data = error.details as Record<string, unknown>;
        if (typeof data.field === 'string' && typeof data.code === 'string' && typeof data.message === 'string') {
            return data as unknown as PrivacyValidation;
        }
    }
}

export const privacyRulesKey = ['settings', 'privacy-rules'] as const;
const privacyMutationKey = [...privacyRulesKey, 'write'] as const;

export function usePrivacyRules() {
    return useQuery({
        queryKey: privacyRulesKey,
        queryFn: ({ signal }) => apiRequest<PrivacyRules>('/api/v1/setting/privacy-rules', { signal }),
        refetchOnMount: 'always',
        refetchInterval: 30000,
    });
}

export function usePrivacyBusy() {
    const rules = useIsMutating({ mutationKey: privacyMutationKey });
    const settings = useIsMutating({ mutationKey: ['settings', 'set'] });
    return rules + settings > 0;
}

export function usePrivacyMutation() {
    const client = useQueryClient();
    return useMutation({
        mutationKey: privacyMutationKey,
        scope: { id: 'privacy-rules' },
        mutationFn: (body: PrivacyMutation) => apiRequest<PrivacyRules>('/api/v1/setting/privacy-rules', { method: 'POST', body }),
        onSuccess: async (data) => {
            client.setQueryData(privacyRulesKey, data);
            await client.invalidateQueries({ queryKey: ['settings', 'list'] });
        },
        onError: async (error) => {
            if (error instanceof ApiError && error.status === 409) {
                await client.invalidateQueries({ queryKey: privacyRulesKey });
            }
        },
    });
}

// Sample text is deliberately kept out of the shared query/mutation cache.
export function previewPrivacy(body: PrivacyPreviewRequest, signal: AbortSignal) {
    return apiRequest<PrivacyPreviewResult>('/api/v1/setting/privacy-preview', { method: 'POST', body, signal });
}
