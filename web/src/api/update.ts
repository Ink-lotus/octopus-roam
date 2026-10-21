import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { create } from 'zustand';
import { ApiError, apiRequest } from './client';

/**
 * 后端 /api/v1/update 返回的最新发布信息
 */
export type UpdateStatus =
    | 'available'
    | 'latest'
    | 'no_release'
    | 'development'
    | 'request_failed'
    | 'unsupported_platform'
    | 'container'
    | 'update_pending'
    | 'update_failed';

export type UpdatePhase = 'idle' | 'pending' | 'completed' | 'failed' | 'unconfirmed';

export interface UpdateAttempt {
    targetVersion: string;
    previousVersion: string;
    deadline: number;
}

export interface UpdateRequest {
    targetVersion: string;
    previousVersion: string;
}

export interface UpdateReceipt {
    status: 'update_pending';
    target_version: string;
}

class InvalidUpdateReceiptError extends Error {}

export const UPDATE_CONFIRM_WINDOW_MS = 30000;

const useUpdateState = create<{ attempt: UpdateAttempt | null; phase: UpdatePhase }>(() => ({
    attempt: null,
    phase: 'idle',
}));

export interface LatestInfo {
    current_version: string;
    tag_name: string;
    published_at: string;
    body: string;
    message: string;
    status: UpdateStatus;
    can_update: boolean;
}

/**
 * Update phase decision table:
 * - no attempt -> idle
 * - update_failed -> failed
 * - the current version equals the accepted target -> completed
 * - unchanged version inside the 30 second window -> pending
 * - unchanged version after the window -> unconfirmed
 */
export function resolveUpdatePhase(
    attempt: UpdateAttempt | null,
    nowVersion: string,
    status: UpdateStatus | undefined,
    now: number,
): UpdatePhase {
    if (!attempt) return 'idle';
    if (status === 'update_failed') return 'failed';
    if (nowVersion && nowVersion === attempt.targetVersion) {
        return 'completed';
    }
    if (now >= attempt.deadline) return 'unconfirmed';
    return 'pending';
}

export function resetUpdateAttempt() {
    useUpdateState.setState({ attempt: null, phase: 'idle' });
}

/**
 * 获取最新发布信息 Hook
 * 
 * @example
 * const { data: latestInfo, isLoading, error } = useLatestInfo();
 * 
 * if (isLoading) return <Loading />;
 * if (error) return <Error message={error.message} />;
 * 
 * console.log('Latest tag:', latestInfo?.tag_name);
 */
export function useLatestInfo() {
    const phase = useUpdateState(state => state.phase);
    return useQuery({
        queryKey: ['update', 'latest'],
        queryFn: () => apiRequest<LatestInfo>('/api/v1/update'),
        refetchInterval: phase === 'pending' ? 1000 : 3600000,
        refetchOnMount: 'always',
    });
}

/**
 * 获取后端当前版本 Hook
 *
 * 后端: GET /api/v1/update/now-version -> string
 */
export function useNowVersion() {
    const phase = useUpdateState(state => state.phase);
    return useQuery({
        queryKey: ['update', 'now-version'],
        queryFn: () => apiRequest<string>('/api/v1/update/now-version'),
        refetchInterval: phase === 'pending' ? 1000 : 3600000,
        refetchOnMount: 'always',
    });
}

export function useUpdateProgress(): UpdatePhase {
    const attempt = useUpdateState(state => state.attempt);
    const nowVersionQuery = useNowVersion();
    const latestInfoQuery = useLatestInfo();
    const [now, setNow] = useState(() => Date.now());
    const currentPhase = resolveUpdatePhase(
        attempt,
        nowVersionQuery.data || '',
        latestInfoQuery.data?.status,
        now,
    );

    useEffect(() => {
        useUpdateState.setState({ phase: currentPhase });
    }, [currentPhase]);

    useEffect(() => {
        if (currentPhase !== 'pending') return undefined;
        const timer = window.setInterval(() => setNow(Date.now()), 1000);
        return () => window.clearInterval(timer);
    }, [currentPhase]);

    return currentPhase;
}

/**
 * 执行更新 Hook
 * 
 * @example
 * const updateCore = useUpdateCore();
 * 
 * updateCore.mutate({ targetVersion: 'v0.1.0', previousVersion: 'v0.1.0-rc.1' }, {
 *   onSuccess: () => {
 *     console.log('Update started successfully');
 *   },
 * });
 */
export function useUpdateCore() {
    const queryClient = useQueryClient();
    const refreshVersions = () => Promise.all([
        queryClient.invalidateQueries({ queryKey: ['update', 'latest'] }),
        queryClient.invalidateQueries({ queryKey: ['update', 'now-version'] }),
    ]);

    return useMutation({
        mutationFn: async (attempt: UpdateRequest) => {
            const receipt = await apiRequest<UpdateReceipt>('/api/v1/update', {
                method: 'POST',
                body: { target_version: attempt.targetVersion },
            });
            if (receipt?.status !== 'update_pending'
                || typeof receipt.target_version !== 'string'
                || !receipt.target_version
                || receipt.target_version !== attempt.targetVersion) {
                throw new InvalidUpdateReceiptError('Invalid update receipt');
            }
            return receipt;
        },
        onSuccess: (receipt, attempt) => {
            useUpdateState.setState({
                attempt: {
                    targetVersion: receipt.target_version,
                    previousVersion: attempt.previousVersion,
                    deadline: Date.now() + UPDATE_CONFIRM_WINDOW_MS,
                },
                phase: 'pending',
            });
            return refreshVersions();
        },
        onError: (error) => {
            if ((error instanceof ApiError && error.status === 409) || error instanceof InvalidUpdateReceiptError) {
                return refreshVersions();
            }
        },
    });
}
