import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Database, Download, Upload } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { toast } from 'sonner';
import { exportEncryptedBackup, useImportDB, type DBImportResult } from '@/api/setting';

export function SettingBackup() {
    const t = useTranslations('setting');

    const importDB = useImportDB();

    const [file, setFile] = useState<File | null>(null);
    const fileInputRef = useRef<HTMLInputElement | null>(null);
    const requestRef = useRef<AbortController | null>(null);
    const [exportPassword, setExportPassword] = useState('');
    const [confirmPassword, setConfirmPassword] = useState('');
    const [importPassword, setImportPassword] = useState('');
    const [busy, setBusy] = useState<'export' | 'import' | null>(null);
    const [result, setResult] = useState<DBImportResult | null>(null);
    useEffect(() => () => requestRef.current?.abort(), []);

    const rowsAffected = result?.rows_affected ?? null;
    const rowsAffectedList = useMemo(() => {
        if (!rowsAffected) return [];
        return Object.entries(rowsAffected)
            .sort(([a], [b]) => a.localeCompare(b))
            .map(([k, v]) => ({ table: k, count: v }));
    }, [rowsAffected]);

    const onPickFile = (f: File | null) => {
        setFile(f);
        setResult(null);
        setImportPassword('');
    };

    const onImport = async () => {
        if (requestRef.current) return;
        if (!file) {
            toast.error(t('backup.import.noFile'));
            setImportPassword('');
            return;
        }
        const controller = new AbortController();
        requestRef.current = controller;
        setBusy('import');
        setResult(null);
        try {
            setResult(await importDB(file, importPassword, controller.signal));
            toast.success(t('backup.import.success'));
            if (fileInputRef.current) fileInputRef.current.value = '';
            setFile(null);
        } catch (e) {
            if (!controller.signal.aborted) toast.error(e instanceof Error ? e.message : t('backup.import.failed'));
        } finally {
            setImportPassword('');
            setBusy(null);
            requestRef.current = null;
        }
    };

    const onExport = async () => {
        if (requestRef.current) return;
        if ([...exportPassword].length < 12 || new TextEncoder().encode(exportPassword).length > 1024) {
            toast.error(t('backup.password.invalid'));
            setExportPassword('');
            setConfirmPassword('');
            return;
        }
        if (exportPassword !== confirmPassword) {
            toast.error(t('backup.password.mismatch'));
            setExportPassword('');
            setConfirmPassword('');
            return;
        }
        const controller = new AbortController();
        requestRef.current = controller;
        setBusy('export');
        try {
            await exportEncryptedBackup(exportPassword, controller.signal);
            toast.success(t('backup.export.success'));
        } catch (e) {
            if (!controller.signal.aborted) toast.error(e instanceof Error ? e.message : t('backup.export.failed'));
        } finally {
            setExportPassword('');
            setConfirmPassword('');
            setBusy(null);
            requestRef.current = null;
        }
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Database className="h-5 w-5" />
                {t('backup.title')}
            </h2>

            {/* 导出 */}
            <div className="space-y-3">
                <p id="backup-password-help" className="text-sm text-muted-foreground">{t('backup.password.help')}</p>
                <label htmlFor="backup-export-password" className="block text-sm">{t('backup.password.export')}</label>
                <Input id="backup-export-password" type="password" autoComplete="new-password" aria-describedby="backup-password-help"
                    value={exportPassword} onChange={(e) => setExportPassword(e.target.value)} disabled={busy !== null} />
                <label htmlFor="backup-confirm-password" className="block text-sm">{t('backup.password.confirm')}</label>
                <Input id="backup-confirm-password" type="password" autoComplete="new-password"
                    value={confirmPassword} onChange={(e) => setConfirmPassword(e.target.value)} disabled={busy !== null} />
                <Button
                    type="button"
                    variant="outline"
                    className="w-full rounded-xl"
                    onClick={onExport}
                    disabled={busy !== null}
                >
                    <Download className="size-4" />
                    {busy === 'export' ? t('backup.export.exporting') : t('backup.export.button')}
                </Button>
            </div>

            <div className="h-px bg-border" />

            {/* 导入 */}
            <div className="space-y-3">
                <div className="text-sm font-semibold text-card-foreground">{t('backup.import.title')}</div>

                <Input
                    ref={fileInputRef}
                    aria-label={t('backup.import.title')}
                    disabled={busy !== null}
                    type="file"
                    accept="application/json,.json"
                    onChange={(e) => onPickFile(e.target.files?.[0] ?? null)}
                    className="rounded-xl"
                />

                <label htmlFor="backup-import-password" className="block text-sm">{t('backup.password.import')}</label>
                <Input id="backup-import-password" type="password" autoComplete="off" aria-describedby="backup-import-help"
                    value={importPassword} onChange={(e) => setImportPassword(e.target.value)} disabled={busy !== null} />
                <p id="backup-import-help" className="text-sm text-muted-foreground">{t('backup.password.legacy')}</p>

                <Button
                    type="button"
                    variant="destructive"
                    className="w-full rounded-xl"
                    onClick={onImport}
                    disabled={busy !== null}
                >
                    <Upload className="size-4" />
                    {busy === 'import' ? t('backup.import.importing') : t('backup.import.button')}
                </Button>

                {rowsAffectedList.length > 0 && (
                    <div className="mt-2 space-y-1">
                        <div className="text-xs font-semibold text-card-foreground">{t('backup.import.result')}</div>
                        <div className="grid grid-cols-2 gap-1 text-xs text-muted-foreground">
                            {rowsAffectedList.map((it) => (
                                <div key={it.table} className="flex justify-between gap-2">
                                    <span className="truncate">{it.table}</span>
                                    <span className="tabular-nums">{it.count}</span>
                                </div>
                            ))}
                        </div>
                    </div>
                )}
            </div>
        </div>
    );
}
