import { queryOptions, useMutation, useQuery } from '@tanstack/react-query';
import { apiRequest, queryClient } from './client';
import { compareRouteVersions } from './group-cache';

export interface GroupCategory {
    id: number;
    name: string;
}

export interface GroupCategorySnapshot {
    version: string;
    items: GroupCategory[];
}

const categoryListKey = ['group-categories', 'list'] as const;

export function shareGroupCategories(previous: unknown, incoming: unknown): GroupCategorySnapshot {
    let current = previous as GroupCategorySnapshot | undefined;
    const cached = queryClient.getQueryData<GroupCategorySnapshot>(categoryListKey);
    if (cached && (!current || compareRouteVersions(cached.version, current.version) > 0)) current = cached;
    const next = incoming as GroupCategorySnapshot;
    // 空数组也是有效快照：删除最后一个分类必须能清空旧列表。
    return current && compareRouteVersions(next.version, current.version) < 0 ? current : next;
}

export function writeGroupCategories(snapshot: GroupCategorySnapshot) {
    queryClient.setQueryData(categoryListKey, (current: GroupCategorySnapshot | undefined) => shareGroupCategories(current, snapshot));
}

export const groupCategoryListQueryOptions = queryOptions({
    queryKey: categoryListKey,
    queryFn: ({ signal }) => apiRequest<GroupCategorySnapshot>('/api/v1/group-category/list', { signal }),
    structuralSharing: shareGroupCategories,
});

export function useGroupCategories() {
    return useQuery(groupCategoryListQueryOptions);
}

export function invalidateGroupCategories() {
    return queryClient.invalidateQueries({ queryKey: categoryListKey });
}

export function useCreateGroupCategory() {
    return useMutation({
        mutationFn: (name: string) => apiRequest<GroupCategory>('/api/v1/group-category/create', { method: 'POST', body: { name } }),
        onSuccess: () => invalidateGroupCategories(),
    });
}

export function useUpdateGroupCategory() {
    return useMutation({
        mutationFn: ({ id, name }: GroupCategory) => apiRequest<GroupCategory>(`/api/v1/group-category/update/${id}`, { method: 'POST', body: { name } }),
        onSuccess: () => invalidateGroupCategories(),
        onError: () => { void invalidateGroupCategories(); },
    });
}

export function useDeleteGroupCategory() {
    return useMutation({
        mutationFn: (id: number) => apiRequest<{ id: number; uncategorized_group_ids: number[] }>(`/api/v1/group-category/delete/${id}`, { method: 'DELETE' }),
        // CRUD 响应没有版本，不用它猜测归属；由完整 SSE / GET 快照更新。
        onSuccess: () => Promise.all([
            invalidateGroupCategories(),
            queryClient.invalidateQueries({ queryKey: ['groups', 'list'] }),
            queryClient.invalidateQueries({ queryKey: ['groups', 'detail'] }),
        ]),
        onError: () => { void invalidateGroupCategories(); },
    });
}
