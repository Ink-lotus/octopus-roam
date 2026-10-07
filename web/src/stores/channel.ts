import { create } from 'zustand';
import { persist } from 'zustand/middleware';

interface ChannelPreferences {
    autoAddGroups: Record<number, boolean>;
    setAutoAddGroups: (channelId: number, enabled: boolean) => void;
}

// 手动刷新选项按渠道保存在浏览器中，未设置时默认添加匹配模型。
export const useChannelPreferences = create<ChannelPreferences>()(
    persist(
        (set) => ({
            autoAddGroups: {},
            setAutoAddGroups: (channelId, enabled) => set((state) => ({
                autoAddGroups: { ...state.autoAddGroups, [channelId]: enabled },
            })),
        }),
        { name: 'octopus-channel-preferences' },
    ),
);
