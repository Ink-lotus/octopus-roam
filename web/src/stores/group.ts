import { create } from 'zustand';
import { persist } from 'zustand/middleware';

interface GroupViewState {
    category: string;
    collapsed: Record<string, boolean>;
    setCategory: (category: string) => void;
    toggleCollapsed: (category: string) => void;
}

// 只保存浏览器视图偏好。分类名称、实体和分组归属始终来自服务端。
export const useGroupViewStore = create<GroupViewState>()(persist((set) => ({
    category: 'all',
    collapsed: {},
    setCategory: (category) => set({ category }),
    toggleCollapsed: (category) => set((state) => ({
        collapsed: { ...state.collapsed, [category]: !state.collapsed[category] },
    })),
}), {
    name: 'group-view-storage',
    partialize: ({ category, collapsed }) => ({ category, collapsed }),
}));
