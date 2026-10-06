import {
    MorphingDialogDescription,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { useState } from 'react';
import { useCreateGroup, useGroupRouteDefaults, type GroupRelayConfig } from '@/api/group';
import { useTranslations } from 'use-intl';
import { GroupEditor } from './Editor';
import { Button } from '@/components/ui/button';
import { toast } from 'sonner';

export function CreateDialogContent() {
    const { setIsOpen } = useMorphingDialog();
    const createGroup = useCreateGroup();
    const defaults = useGroupRouteDefaults();
    const [initialConfig, setInitialConfig] = useState<GroupRelayConfig | null>(null);
    const t = useTranslations('group');

    // 首次读取成功后固定表单初值; 后台刷新或导入不会覆盖用户正在编辑的内容。
    if (initialConfig === null && defaults.isFetchedAfterMount && defaults.isSuccess) {
        setInitialConfig(defaults.data);
    }

    return (
        <div className="w-screen max-w-full md:max-w-4xl h-[calc(100vh-2rem)] min-h-0 flex flex-col">
            <MorphingDialogDescription className="flex-1 min-h-0 overflow-hidden">
                {initialConfig === null ? (
                    <div className="flex h-full flex-col items-center justify-center gap-4 p-6 text-center">
                        <p role={defaults.isError ? 'alert' : 'status'} className="text-sm text-muted-foreground">
                            {defaults.isError ? t('create.defaultsFailed') : t('create.defaultsLoading')}
                        </p>
                        {defaults.isError && (
                            <Button type="button" variant="outline" onClick={() => void defaults.refetch()} disabled={defaults.isFetching}>
                                {t('create.retry')}
                            </Button>
                        )}
                        <Button type="button" variant="secondary" onClick={() => setIsOpen(false)}>{t('detail.actions.cancel')}</Button>
                    </div>
                ) : <GroupEditor
                    initial={{ relay_config: initialConfig }}
                    submitText={t('create.submit')}
                    submittingText={t('create.submitting')}
                    isSubmitting={createGroup.isPending}
                    onCancel={() => setIsOpen(false)}
                    onSubmit={({ name, mode, model_match_mode, relay_config, members }) => {
                        createGroup.mutate(
                            {
                                name,
                                mode,
                                model_match_mode,
                                relay_config,
                                items: members.map((member) => ({ channel_grant_id: member.channel_grant_id })),
                            },
                            {
                                onSuccess: () => setIsOpen(false),
                                onError: (error) => toast.error(t('toast.createFailed'), { description: error.message }),
                            }
                        );
                    }}
                />}
            </MorphingDialogDescription>
        </div>
    );
}
