import { useEffect, useMemo } from 'react';
import { ArrowUpAZ, ChevronDown } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { GroupCard } from './Card';
import { CreateDialogContent } from './Create';
import { useRuntimeClock } from './MemberStatus';
import { useGroupList } from '@/api/group';
import { PageActions, usePageActionsStore } from '@/components/common/PageActions';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import { RefreshAllButton } from '@/components/modules/channel/RefreshAllButton';
import { useGroupCategories } from '@/api/group-category';
import { useGroupViewStore } from '@/stores/group';
import { Button } from '@/components/ui/button';
import { CategoryManager } from './CategoryManager';
import { CategorySelect } from './CategorySelect';

// GroupActions 向稳定顶栏提供分组页面的搜索、视图选项和创建入口。
export function GroupActions() {
    const t = useTranslations('toolbar');
    const searchTerm = usePageActionsStore((state) => state.searchTerms.group || '');
    const sortOrder = usePageActionsStore((state) => state.sortOrders.group === 'desc' ? 'desc' : 'asc');
    const filter = usePageActionsStore((state) => state.groupFilter);
    const setSearchTerm = usePageActionsStore((state) => state.setSearchTerm);
    const setSort = usePageActionsStore((state) => state.setSort);
    const setFilter = usePageActionsStore((state) => state.setGroupFilter);

    return (
        <PageActions
            searchTerm={searchTerm}
            onSearchTermChange={(value) => setSearchTerm('group', value)}
            sortOptions={[
                { value: 'asc', label: t('popover.nameAsc'), icon: ArrowUpAZ },
                { value: 'desc', label: t('popover.nameDesc'), icon: ArrowUpAZ },
            ]}
            sortValue={sortOrder}
            onSortChange={(value) => {
                if (value === 'asc' || value === 'desc') setSort('group', value);
            }}
            filterOptions={[
                { value: 'all', label: t('popover.filter.group.all') },
                { value: 'with-members', label: t('popover.filter.group.withMembers') },
                { value: 'empty', label: t('popover.filter.group.empty') },
            ]}
            filterValue={filter}
            onFilterChange={(value) => {
                if (value === 'all' || value === 'with-members' || value === 'empty') setFilter(value);
            }}
            actions={<RefreshAllButton />}
        >
            <CreateDialogContent />
        </PageActions>
    );
}

// Group 渲染分组列表正文。
export function Group() {
    const groupQuery = useGroupList(true, true);
    const { data: groups } = groupQuery;
    const categories = useGroupCategories();
    const t = useTranslations('group.category');
    const runtimeNow = useRuntimeClock(groups);
    const searchTerm = usePageActionsStore((state) => state.searchTerms.group || '');
    const sortOrder = usePageActionsStore((state) => state.sortOrders.group === 'desc' ? 'desc' : 'asc');
    const filter = usePageActionsStore((state) => state.groupFilter);
    const category = useGroupViewStore((state) => state.category);
    const collapsed = useGroupViewStore((state) => state.collapsed);
    const setCategory = useGroupViewStore((state) => state.setCategory);
    const toggleCollapsed = useGroupViewStore((state) => state.toggleCollapsed);

    const categoryExists = category === 'all' || category === 'uncategorized'
        || categories.data?.items.some((item) => String(item.id) === category);
    const selectedCategory = categoryExists ? category : 'all';
    useEffect(() => {
        if (categories.isSuccess && !categoryExists) setCategory('all');
    }, [categories.isSuccess, categoryExists, setCategory]);

    const sortedGroups = useMemo(() => {
        if (!groups) return [];
        return [...groups].sort((a, b) =>
            (sortOrder === 'asc' ? a.name.localeCompare(b.name) : b.name.localeCompare(a.name)) || a.id - b.id
        );
    }, [groups, sortOrder]);

    const visibleGroups = useMemo(() => {
        const term = searchTerm.toLowerCase().trim();
        const byName = !term ? sortedGroups : sortedGroups.filter((g) => g.name.toLowerCase().includes(term));

        if (filter === 'with-members') return byName.filter((g) => (g.items?.length || 0) > 0);
        if (filter === 'empty') return byName.filter((g) => (g.items?.length || 0) === 0);

        return byName;
    }, [sortedGroups, searchTerm, filter]);

    const sections = useMemo(() => {
        const sorted = [...(categories.data?.items ?? [])].sort((a, b) =>
            (sortOrder === 'asc' ? a.name.localeCompare(b.name) : b.name.localeCompare(a.name)) || a.id - b.id);
        const buckets = new Map<string, typeof visibleGroups>();
        for (const group of visibleGroups) {
            const key = group.category_id === null ? 'uncategorized' : String(group.category_id);
            const bucket = buckets.get(key);
            if (bucket) bucket.push(group);
            else buckets.set(key, [group]);
        }
        return [...sorted.map((item) => ({ key: String(item.id), name: item.name })), { key: 'uncategorized', name: t('uncategorized') }]
            .filter((item) => selectedCategory === 'all' || item.key === selectedCategory)
            .map((item) => {
                const members = buckets.get(item.key) ?? [];
                const isCollapsed = !!collapsed[item.key];
                return {
                    key: item.key,
                    items: members,
                    collapsed: isCollapsed,
                    header: <div className="rounded-2xl border bg-muted/30" data-category-id={item.key}>
                        <h2>
                            <button type="button" aria-expanded={!isCollapsed}
                                aria-label={t(isCollapsed ? 'expand' : 'collapse', { name: item.name, count: members.length })}
                                onClick={() => toggleCollapsed(item.key)}
                                className="flex w-full items-center gap-3 rounded-2xl p-3 text-left focus-visible:outline-2 focus-visible:outline-ring">
                                <ChevronDown className={`size-4 shrink-0 transition-transform ${isCollapsed ? '-rotate-90' : ''}`} aria-hidden="true" />
                                <span className="min-w-0 flex-1 break-all font-semibold">{item.name}</span>
                                <span className="shrink-0 text-xs text-muted-foreground">{t('count', { count: members.length })}</span>
                            </button>
                        </h2>
                        {!isCollapsed && members.length === 0 && <p className="px-4 pb-3 text-sm text-muted-foreground">{t('noMatches')}</p>}
                    </div>,
                };
            });
    }, [categories.data, sortOrder, visibleGroups, selectedCategory, collapsed, toggleCollapsed, t]);

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            <div className="shrink-0 space-y-2 px-1">
                <div className="flex flex-wrap items-end gap-2">
                    <div className="min-w-0 flex-1 basis-40 max-w-sm space-y-1">
                        <label htmlFor="category-filter" className="text-xs font-medium text-muted-foreground">{t('label')}</label>
                        <CategorySelect id="category-filter" value={selectedCategory} onChange={setCategory}
                            categories={categories.data?.items ?? []} includeAll disabled={categories.isPending} />
                    </div>
                    <CategoryManager />
                </div>
                <p className="text-xs text-muted-foreground">{t('countHint')}</p>
                {(categories.isError || groupQuery.isError) && <p role="alert" className="text-sm text-destructive">
                    {t('loadFailed')} <Button type="button" variant="outline" onClick={() => { void categories.refetch(); void groupQuery.refetch(); }}>{t('retry')}</Button>
                </p>}
                {(categories.isPending || groupQuery.isPending) && <p role="status" className="text-sm text-muted-foreground">{t('loading')}</p>}
            </div>
            <div className="min-h-0 flex-1">
                <VirtualizedGrid
                    items={visibleGroups}
                    sections={categories.data ? sections : []}
                    resetKey={`${selectedCategory}:${sortOrder}:${filter}:${searchTerm}`}
                    ariaLabel={t('browse')}
                    columns={{ default: 1, md: 2, lg: 3 }}
                    estimateItemHeight={520}
                    getItemKey={(group) => group.id}
                    renderItem={(group) => <GroupCard group={group} now={runtimeNow} />}
                />
            </div>
        </div>
    );
}
