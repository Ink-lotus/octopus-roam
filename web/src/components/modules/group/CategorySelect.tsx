import { useRef, useState } from 'react';
import { Plus } from 'lucide-react';
import { useTranslations } from 'use-intl';
import type { GroupCategory } from '@/api/group-category';
import { Select, SelectContent, SelectItem, SelectSeparator, SelectTrigger, SelectValue } from '@/components/ui/select';
import { CategoryManager } from './CategoryManager';

export function CategorySelect({
    id, value, onChange, categories, disabled, includeAll = false, allowCreate = false, invalid = false, describedBy,
}: {
    id: string;
    value: string;
    onChange: (value: string) => void;
    categories: GroupCategory[];
    disabled?: boolean;
    includeAll?: boolean;
    allowCreate?: boolean;
    invalid?: boolean;
    describedBy?: string;
}) {
    const t = useTranslations('group.category');
    const [managerOpen, setManagerOpen] = useState(false);
    const triggerRef = useRef<HTMLButtonElement>(null);
    const missing = value !== 'all' && value !== 'uncategorized' && !categories.some((category) => String(category.id) === value);
    return (
        <>
            <Select value={value} onValueChange={(next) => {
                if (allowCreate && next === 'create-category') setManagerOpen(true);
                else onChange(next);
            }} disabled={disabled}>
                <SelectTrigger ref={triggerRef} id={id} aria-label={includeAll ? t('filter') : t('label')} aria-invalid={invalid || missing || undefined}
                    aria-describedby={describedBy} className="w-full min-w-0 rounded-xl [&>span]:truncate">
                    <SelectValue />
                </SelectTrigger>
                <SelectContent onEscapeKeyDown={(event) => event.stopPropagation()}
                    onCloseAutoFocus={(event) => { if (managerOpen) event.preventDefault(); }}>
                    {includeAll && <SelectItem value="all">{t('all')}</SelectItem>}
                    {allowCreate && <>
                        <SelectItem value="create-category" textValue={t('create')}>
                            <Plus className="size-4" aria-hidden="true" />{t('create')}
                        </SelectItem>
                        <SelectSeparator />
                    </>}
                    <SelectItem value="uncategorized">{t('uncategorized')}</SelectItem>
                    {missing && <SelectItem value={value} disabled>{t('missing')}</SelectItem>}
                    {[...categories].sort((a, b) => a.name.localeCompare(b.name) || a.id - b.id).map((category) => (
                        <SelectItem key={category.id} value={String(category.id)} className="break-all">{category.name}</SelectItem>
                    ))}
                </SelectContent>
            </Select>
            {allowCreate && <CategoryManager open={managerOpen} onOpenChange={setManagerOpen}
                onCloseAutoFocus={(event) => { event.preventDefault(); triggerRef.current?.focus(); }} />}
        </>
    );
}
