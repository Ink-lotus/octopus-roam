import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

export interface WebhookEndpoint {
    id: number;
    name: string;
    enabled: boolean;
    url: string;
    method: string;
    content_type: string;
    headers: Record<string, string>;
    body_template: string;
    timeout_seconds: number;
}

export interface WebhookSendResult {
    success: boolean;
    status_code?: number;
    error?: string;
}

export const webhookQueryKey = ['webhooks', 'list'];

export function useWebhookEndpointList() {
    return useQuery({
        queryKey: webhookQueryKey,
        queryFn: () => apiRequest<WebhookEndpoint[]>('/api/v1/webhook/list'),
        refetchOnMount: 'always',
    });
}

function useWebhookMutation<T>(path: (value: T) => string, method: 'POST' | 'DELETE') {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ endpoint, body }: { endpoint: T; body?: unknown }) =>
            apiRequest(path(endpoint), { method, body }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: webhookQueryKey }),
    });
}

export function useCreateWebhookEndpoint() {
    return useWebhookMutation<Omit<WebhookEndpoint, 'id'>>(() => '/api/v1/webhook/create', 'POST');
}

export function useUpdateWebhookEndpoint() {
    return useWebhookMutation<WebhookEndpoint>((endpoint) => `/api/v1/webhook/update/${endpoint.id}`, 'POST');
}

export function useDeleteWebhookEndpoint() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (id: number) => apiRequest<void>(`/api/v1/webhook/delete/${id}`, { method: 'DELETE' }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: webhookQueryKey }),
    });
}

export function useTestWebhookEndpoint() {
    return useMutation({
        mutationFn: (id: number) => apiRequest<WebhookSendResult>(`/api/v1/webhook/test/${id}`, { method: 'POST', body: {} }),
    });
}
