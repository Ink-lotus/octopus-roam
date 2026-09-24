import { useTranslations } from 'use-intl';
import type { ReactNode } from 'react';
import { CopyIconButton } from '@/components/common/CopyButton';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { isValidRegex } from '@/lib/regex';

const examplePatterns = [
    { key: 'prefix', pattern: '^gpt-' },
    { key: 'suffix', pattern: '-latest$' },
    { key: 'includeKeyword', pattern: 'claude' },
    { key: 'excludeKeyword', pattern: '^(?!.*preview).+$' },
    { key: 'excludeEmbeddingRerank', pattern: '^(?!.*(?:embedding|rerank)).+$' },
] as const;

export function RegexFilterField({
    id,
    label,
    labelIcon,
    value,
    validationValue,
    placeholder,
    onChange,
    onBlur,
}: {
    id: string;
    label: string;
    labelIcon?: ReactNode;
    value: string;
    validationValue?: string;
    placeholder?: string;
    onChange: (value: string) => void;
    onBlur?: () => void;
}) {
    const t = useTranslations('regexFilter');
    const isValid = isValidRegex(validationValue ?? value);
    const errorId = `${id}-error`;

    return (
        <div className="space-y-3">
            <div className="space-y-2">
                <div className="flex items-center gap-2">
                    {labelIcon}
                    <Label htmlFor={id}>{label}</Label>
                </div>
                <Input
                    id={id}
                    value={value}
                    onChange={(event) => onChange(event.target.value)}
                    onBlur={onBlur}
                    placeholder={placeholder}
                    aria-invalid={!isValid}
                    aria-describedby={!isValid ? errorId : undefined}
                    className="rounded-xl font-mono text-sm"
                />
                <p className="text-xs text-muted-foreground">{t('syntax')}</p>
                <p className="text-xs text-muted-foreground">{t('combined')}</p>
                <p className="text-xs text-muted-foreground">{t('empty')}</p>
                {!isValid && (
                    <p id={errorId} role="alert" className="text-sm text-destructive">
                        {t('invalid')}
                    </p>
                )}
            </div>

            <div className="space-y-2">
                <h3 className="text-sm font-medium">{t('examples.title')}</h3>
                <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                    {examplePatterns.map(({ key, pattern }) => (
                        <div key={key} className="flex min-w-0 items-center gap-2 rounded-md border border-border px-3 py-2">
                            <div className="min-w-0 flex-1">
                                <p className="text-xs text-muted-foreground">{t(`examples.${key}`)}</p>
                                <code className="block truncate font-mono text-sm" title={pattern}>{pattern}</code>
                            </div>
                            <CopyIconButton
                                text={pattern}
                                ariaLabel={t('copy')}
                                className="flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                                copyIconClassName="size-4"
                                checkIconClassName="size-4 text-primary"
                            />
                        </div>
                    ))}
                </div>
            </div>
        </div>
    );
}
