import { useTranslations } from 'use-intl';
import type { GroupCategory } from '@/api/group-category';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';

export function CategorySelect({
    id, value, onChange, categories, disabled, includeAll = false, invalid = false, describedBy,
}: {
    id: string;
    value: string;
    onChange: (value: string) => void;
    categories: GroupCategory[];
    disabled?: boolean;
    includeAll?: boolean;
    invalid?: boolean;
    describedBy?: string;
}) {
    const t = useTranslations('group.category');
    const missing = value !== 'all' && value !== 'uncategorized' && !categories.some((category) => String(category.id) === value);
    return (
        <Select value={value} onValueChange={onChange} disabled={disabled}>
            <SelectTrigger id={id} aria-label={includeAll ? t('filter') : t('label')} aria-invalid={invalid || missing || undefined}
                aria-describedby={describedBy} className="w-full min-w-0 rounded-xl [&>span]:truncate">
                <SelectValue />
            </SelectTrigger>
            <SelectContent>
                {includeAll && <SelectItem value="all">{t('all')}</SelectItem>}
                <SelectItem value="uncategorized">{t('uncategorized')}</SelectItem>
                {missing && <SelectItem value={value} disabled>{t('missing')}</SelectItem>}
                {[...categories].sort((a, b) => a.name.localeCompare(b.name) || a.id - b.id).map((category) => (
                    <SelectItem key={category.id} value={String(category.id)} className="break-all">{category.name}</SelectItem>
                ))}
            </SelectContent>
        </Select>
    );
}
