import type { GroupModelMatchMode } from '@/api/group';

export function normalizeKey(value: string) {
    return value.trim().toLowerCase();
}

export function memberKey(member: { channel_grant_id: number }) {
    return String(member.channel_grant_id);
}

export function matchesGroupName(modelName: string, groupName: string, mode: GroupModelMatchMode = 'keyword') {
    if (!groupName.trim()) return false;
    if (mode === 'exact') {
        return modelName.trim().toLowerCase() === groupName.trim().toLowerCase();
    }
    return modelName.toLowerCase().includes(groupName.trim().toLowerCase());
}
