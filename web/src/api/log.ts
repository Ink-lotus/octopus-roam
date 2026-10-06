import { useMutation, useQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { apiRequest } from './client';

// RequestState 表示 Relay 请求的实时状态。
export type RequestState = 'running' | 'committed' | 'success' | 'failed' | 'canceled';
export type RelayChannelStatus = 'waiting' | 'normal' | 'error';

// RelayUsage 保存请求结束后确认的统一 Token 用量。
export interface RelayUsage {
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
    prompt_tokens_details: {
        cached_tokens: number;
        write_cached_tokens?: number;
    } | null;
}

// RelayLogOverview 是请求状态流发送的完整进程内请求状态。
export interface RelayLogOverview {
    id: number;
    status: RequestState;
    started_at: string;
    duration: number;
    first_token_duration: number;
    stream_duration: number;
    response_duration: number;
    model: string;
    reasoning_effort: string;
    protocol: number;
    group_id: number;
    api_key_name: string;
    usage: RelayUsage;
    cost: number;
    output_chars: number;
    round: number;
    round_started_at: string;
    target_channel: string;
    target_model: string;
    target_protocol: number;
    sending: boolean;
    error?: string;
    retry_count?: number;
    attempt?: number;
    attempt_limit?: number;
    retrying?: boolean;
    error_count?: number;
    last_error?: string;
    next_retry_at?: number;
    channel_status?: RelayChannelStatus;
}

function compareLogOrder(left: RelayLogOverview, right: RelayLogOverview) {
    const leftIsActive = left.status === 'running' || left.status === 'committed';
    const rightIsActive = right.status === 'running' || right.status === 'committed';
    if (leftIsActive !== rightIsActive) return leftIsActive ? -1 : 1;
    if (left.id === right.id) return 0;
    return left.id > right.id ? -1 : 1;
}

// useClearLogs 清空已完成的内存日志。
export function useClearLogs() {
    return useMutation({
        mutationFn: () => apiRequest<null>('/api/v1/log/clear', { method: 'DELETE' }),
    });
}

// useStopRequest 按是否提供轮次参数, 中止单个轮次或整个请求。
export function useStopRequest() {
    return useMutation({
        mutationFn: ({ requestId, round }: { requestId: number; round?: number }) =>
            apiRequest<null>(`/api/v1/log/stop/${requestId}${round === undefined ? '' : `/${round}`}`, { method: 'POST' }),
    });
}

// useLogs 订阅进程内日志概览，并按状态分组、组内 RequestID 倒序排列。
export function useLogs() {
    const [logs, setLogs] = useState<RelayLogOverview[]>([]);
    const [isLoading, setIsLoading] = useState(true);
    const [error, setError] = useState<Error | null>(null);

    useEffect(() => {
        const source = new EventSource('/api/v1/log/overview/stream', { withCredentials: true });

        source.onopen = () => {
            setError(null);
            setIsLoading(false);
        };
        source.addEventListener('log', (event) => {
            let next: RelayLogOverview;
            try {
                next = JSON.parse((event as MessageEvent<string>).data) as RelayLogOverview;
            } catch {
                setError(new Error('Invalid log update'));
                return;
            }
            setIsLoading(false);
            setError(null);
            // SSE 重连或状态变化后也按状态重新归组, 同时保留未变更记录的引用。
            setLogs((current) => {
                const index = current.findIndex((item) => item.id === next.id);
                const updated = current.slice();
                if (index >= 0) {
                    updated[index] = next;
                } else {
                    updated.push(next);
                }
                return updated.sort(compareLogOrder);
            });
        });
        source.onerror = () => {
            setIsLoading(false);
            setError(new Error('Log stream disconnected'));
        };

        return () => {
            source.close();
        };
    }, []);

    return { logs, isLoading, error };
}

// useLogRequestBody 在调用方启用时按需获取指定日志的请求体。
export function useLogRequestBody(id: number, startedAt: string, enabled: boolean) {
    return useQuery({
        queryKey: ['logs', id, startedAt, 'request-body'],
        queryFn: () => apiRequest<string>(`/api/v1/log/request-body/${id}`),
        enabled,
        staleTime: Infinity,
    });
}

// useLogResponseBody 在调用方启用时获取指定日志的最终响应体。
export function useLogResponseBody(id: number, startedAt: string, enabled: boolean) {
    return useQuery({
        queryKey: ['logs', id, startedAt, 'response-body'],
        queryFn: () => apiRequest<string>(`/api/v1/log/response-body/${id}`),
        enabled,
        staleTime: Infinity,
    });
}
