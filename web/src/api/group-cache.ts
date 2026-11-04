import type { Group, GroupRuntime } from './group';
import { apiRequest, queryClient } from './client';

const listKey = ['groups', 'list'] as const;
const detailKey = (id: number) => ['groups', 'detail', id] as const;
// Runtime events may arrive before the configuration snapshot they belong to.
const pendingRuntimes = new Map<number, GroupRuntime>();

function normalizeRuntime(runtime: GroupRuntime): GroupRuntime {
    return {
        ...runtime,
        cooldown_versions: runtime.cooldown_versions ?? {},
        cooldowns: runtime.cooldowns ?? {},
        rule_cooldowns: runtime.rule_cooldowns ?? [],
    };
}

export function compareRouteVersions(left: string, right: string): number {
    if (left === right) return 0;
    const a = left.split(':').map(BigInt);
    const b = right.split(':').map(BigInt);
    for (let index = 0; index < 2; index++) {
        if (a[index] !== b[index]) return a[index] > b[index] ? 1 : -1;
    }
    return 0;
}

function mergeGroup(current: Group | undefined, incoming: Group): Group {
    incoming = { ...incoming, category_id: incoming.category_id ?? null, metadata_version: incoming.metadata_version ?? '0:0', runtime: normalizeRuntime(incoming.runtime) };
    if (!current) return incoming;
    current = { ...current, runtime: normalizeRuntime(current.runtime) };
    const configOrder = compareRouteVersions(incoming.runtime.config_version, current.runtime.config_version);
    const config = configOrder < 0 ? current : incoming;
    const runtime = configOrder === 0 && compareRouteVersions(incoming.runtime.version, current.runtime.version) < 0
        ? current.runtime : config.runtime;
    // 分类移动不推进路由版本；三个维度必须分别合并，包含旧 GET / 新 SSE 的交错。
    const metadata = compareRouteVersions(incoming.metadata_version, current.metadata_version ?? '0:0') < 0
        ? current : incoming;
    return { ...config, runtime, category_id: metadata.category_id ?? null, metadata_version: metadata.metadata_version ?? '0:0' };
}

function latestGroup(incoming: Group, previous?: Group): Group {
    let group = mergeGroup(previous, incoming);
    group = mergeGroup(queryClient.getQueryData<Group>(detailKey(group.id)), group);
    const listed = queryClient.getQueryData<Group[]>(listKey)?.find((item) => item.id === group.id);
    group = mergeGroup(listed, group);
    const pending = pendingRuntimes.get(group.id);
    if (pending && pending.config_version === group.runtime.config_version && compareRouteVersions(pending.version, group.runtime.version) > 0) {
        group = { ...group, runtime: pending };
    }
    return group;
}

// Run reconciliation when React Query commits a read as well as when SSE or a
// mutation updates it. A read already in flight cannot overwrite a newer event.
export function shareGroupSnapshot(previous: unknown, incoming: unknown): Group {
    return latestGroup(incoming as Group, previous as Group | undefined);
}

export function shareGroupList(previous: unknown, incoming: unknown): Group[] {
    const current = previous as Group[] | undefined;
    return (incoming as Group[]).map((group) => latestGroup(group, current?.find((item) => item.id === group.id)));
}

export function writeGroupCache(incoming: Group): Group {
    const group = latestGroup(incoming);
    queryClient.setQueryData<Group[]>(listKey, (current) => {
        if (!current) return current;
        return [...current.filter((item) => item.id !== group.id), group]
            .sort((a, b) => a.name < b.name ? -1 : a.name > b.name ? 1 : 0);
    });
    queryClient.setQueryData(detailKey(group.id), group);
    return group;
}

export function removeGroupCache(id: number) {
    pendingRuntimes.delete(id);
    void queryClient.cancelQueries({ queryKey: listKey });
    void queryClient.cancelQueries({ queryKey: detailKey(id) });
    queryClient.setQueryData<Group[]>(listKey, (current) => current?.filter((item) => item.id !== id));
    queryClient.removeQueries({ queryKey: detailKey(id) });
}

export function writeGroupRuntime(update: GroupRuntime) {
    update = normalizeRuntime(update);
    const pending = pendingRuntimes.get(update.group_id);
    if (pending) {
        const order = compareRouteVersions(update.config_version, pending.config_version);
        if (order < 0 || (order === 0 && compareRouteVersions(update.version, pending.version) <= 0)) return;
    }
    pendingRuntimes.set(update.group_id, update);
    const current = queryClient.getQueryData<Group>(detailKey(update.group_id))
        ?? queryClient.getQueryData<Group[]>(listKey)?.find((group) => group.id === update.group_id);
    if (!current) return;
    const configOrder = compareRouteVersions(update.config_version, current.runtime.config_version);
    if (configOrder > 0) {
        void queryClient.invalidateQueries({ queryKey: listKey });
        void queryClient.invalidateQueries({ queryKey: detailKey(update.group_id) });
    } else if (configOrder === 0) {
        writeGroupCache({ ...current, runtime: update });
    }
}

export function clearPendingGroupRuntimes() {
    pendingRuntimes.clear();
}

export async function readGroupList(signal?: AbortSignal): Promise<Group[]> {
    // 在查询提交时统一合并；不要为每个分组重写、排序整张列表。
    return apiRequest<Group[]>('/api/v1/group/list', { signal });
}

export async function readGroup(id: number, signal?: AbortSignal): Promise<Group> {
    return writeGroupCache(await apiRequest<Group>(`/api/v1/group/get/${id}`, { signal }));
}
