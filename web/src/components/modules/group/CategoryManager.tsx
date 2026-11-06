import { useRef, useState, type FormEvent } from 'react';
import { Dialog } from 'radix-ui';
import { Pencil, Trash2, X } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { ApiError } from '@/api/client';
import { useCreateGroupCategory, useDeleteGroupCategory, useGroupCategories, useUpdateGroupCategory, type GroupCategory } from '@/api/group-category';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';

function categoryErrorKey(error: unknown): 'duplicate' | 'missing' | 'invalidName' | 'saveFailed' {
    if (error instanceof ApiError) {
        if (error.status === 409) return 'duplicate';
        if (error.status === 404) return 'missing';
        if (error.status === 400) return 'invalidName';
    }
    return 'saveFailed';
}

function validName(name: string) {
    const value = name.trim();
    return [...value].length >= 1 && [...value].length <= 64 && !/\p{Cc}/u.test(value);
}

export function CategoryManager({ open, onOpenChange, onCloseAutoFocus }: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onCloseAutoFocus: (event: Event) => void;
}) {
    const t = useTranslations('group.category');
    const query = useGroupCategories();
    const create = useCreateGroupCategory();
    const update = useUpdateGroupCategory();
    const remove = useDeleteGroupCategory();
    const [name, setName] = useState('');
    const [editing, setEditing] = useState<GroupCategory | null>(null);
    const [deleting, setDeleting] = useState<GroupCategory | null>(null);
    const [error, setError] = useState('');
    const [message, setMessage] = useState('');
    const input = useRef<HTMLInputElement>(null);
    const busy = create.isPending || update.isPending || remove.isPending;

    const reset = () => {
        setEditing(null);
        setDeleting(null);
        setName('');
        setError('');
    };
    const save = (event: FormEvent) => {
        event.preventDefault();
        // Portal 仍沿 React 树冒泡，分类提交不能触发外层分组表单。
        event.stopPropagation();
        if (busy) return;
        setMessage('');
        if (!validName(name)) { setError(t('invalidName')); input.current?.focus(); return; }
        const callbacks = {
            onSuccess: () => { reset(); setMessage(t('saved')); input.current?.focus(); },
            onError: (failure: Error) => { setError(t(categoryErrorKey(failure))); input.current?.focus(); },
        };
        if (editing) update.mutate({ id: editing.id, name: name.trim() }, callbacks);
        else create.mutate(name.trim(), callbacks);
    };

    return (
        <Dialog.Root open={open} onOpenChange={(value) => {
            if (busy) return;
            onOpenChange(value);
            reset();
            setMessage('');
        }}>
            <Dialog.Portal>
                <Dialog.Overlay className="fixed inset-0 z-50 bg-black/40" />
                <Dialog.Content onEscapeKeyDown={(event) => event.stopPropagation()} onCloseAutoFocus={onCloseAutoFocus}
                    onOpenAutoFocus={(event) => { event.preventDefault(); input.current?.focus(); }}
                    className="fixed left-1/2 top-1/2 z-50 flex max-h-[calc(100dvh-2rem)] w-[calc(100%-2rem)] max-w-lg -translate-x-1/2 -translate-y-1/2 flex-col gap-4 overflow-y-auto rounded-3xl border bg-card p-5 text-card-foreground shadow-xl">
                    <div className="flex items-center justify-between gap-3">
                        <Dialog.Title className="text-lg font-semibold">{t('manage')}</Dialog.Title>
                        <Dialog.Close asChild><Button type="button" variant="ghost" size="icon" disabled={busy} aria-label={t('close')}><X aria-hidden="true" /></Button></Dialog.Close>
                    </div>
                    <Dialog.Description className="text-sm text-muted-foreground">{t('hint')}</Dialog.Description>
                    <form onSubmit={save} className="space-y-2">
                        <label htmlFor="category-name" className="text-sm font-medium">{editing ? t('renameName', { name: editing.name }) : t('newName')}</label>
                        <Input id="category-name" ref={input} value={name} disabled={busy}
                            onChange={(event) => { setName(event.target.value); setError(''); }}
                            aria-invalid={!!error} aria-describedby={error ? 'category-error' : 'category-name-hint'} />
                        <p id="category-name-hint" className="text-xs text-muted-foreground">{t('nameHint')}</p>
                        <div className="flex gap-2">
                            <Button type="submit" disabled={busy}>{busy ? t('saving') : editing ? t('save') : t('create')}</Button>
                            {editing && <Button type="button" variant="secondary" disabled={busy} onClick={reset}>{t('cancel')}</Button>}
                        </div>
                    </form>
                    {error && <p id="category-error" role="alert" className="text-sm text-destructive">{error}</p>}
                    <p role="status" className="text-sm text-muted-foreground">{message}</p>
                    {query.isError && <div role="alert" className="text-sm text-destructive">{t('loadFailed')} <Button type="button" variant="outline" onClick={() => void query.refetch()}>{t('retry')}</Button></div>}
                    {query.isPending && <p role="status">{t('loading')}</p>}
                    {query.isSuccess && query.data.items.length === 0 && <p className="text-sm text-muted-foreground">{t('none')}</p>}
                    <ul className="space-y-2" aria-label={t('list')}>
                        {[...(query.data?.items ?? [])].sort((a, b) => a.name.localeCompare(b.name) || a.id - b.id).map((category) => (
                            <li key={category.id} className="flex flex-wrap items-center gap-2 rounded-xl border p-3">
                                <span className="min-w-0 flex-1 break-all text-sm font-medium">{category.name}</span>
                                <Button type="button" variant="ghost" size="icon" disabled={busy} aria-label={t('rename', { name: category.name })}
                                    onClick={() => { setEditing(category); setName(category.name); setDeleting(null); setError(''); setMessage(''); input.current?.focus(); }}><Pencil className="size-4" aria-hidden="true" /></Button>
                                <Button type="button" variant="ghost" size="icon" disabled={busy} aria-label={t('delete', { name: category.name })}
                                    onClick={() => { setDeleting(category); setError(''); setMessage(''); }}><Trash2 className="size-4" aria-hidden="true" /></Button>
                                {deleting?.id === category.id && <div className="w-full space-y-2 border-t pt-2">
                                    <p className="text-sm" role="status">{t('deleteHint', { name: category.name })}</p>
                                    <div className="flex gap-2">
                                        <Button type="button" variant="destructive" disabled={busy} onClick={() => remove.mutate(category.id, {
                                            onSuccess: () => { reset(); setMessage(t('deleted')); input.current?.focus(); },
                                            onError: (failure) => setError(t(categoryErrorKey(failure))),
                                        })}>{t('confirmDelete')}</Button>
                                        <Button type="button" variant="secondary" disabled={busy} onClick={() => setDeleting(null)}>{t('cancel')}</Button>
                                    </div>
                                </div>}
                            </li>
                        ))}
                    </ul>
                </Dialog.Content>
            </Dialog.Portal>
        </Dialog.Root>
    );
}
