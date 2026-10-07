import { SettingAppearance } from './Appearance';
import { SettingSystem } from './System';
import { SettingAPIKey } from './APIKey';
import { SettingLLMPrice } from './LLMPrice';
import { SettingAccount } from './Account';
import { SettingInfo } from './Info';
import { SettingLog } from './Log';
import { SettingBackup } from './Backup';
import { SettingWebhook } from './Webhook';
import { SettingRouteDefaults } from './RouteDefaults';
import { SettingAutomation } from './Automation';

// Setting 渲染设置页面正文。
export function Setting() {
    return (
        <div className="h-full min-h-0 overflow-y-auto overscroll-contain rounded-t-3xl pb-24 md:pb-4">
            {/* 两列独立堆叠，避免卡片高度变化时自动重新分栏。 */}
            <div className="grid grid-cols-1 items-start gap-4 md:grid-cols-2">
                <div className="min-w-0 space-y-4">
                    <SettingInfo />
                    <SettingAppearance />
                    <SettingAccount />
                    <SettingSystem />
                    <SettingAutomation />
                    <SettingBackup />
                </div>
                <div className="min-w-0 space-y-4">
                    <SettingRouteDefaults />
                    <SettingLog />
                    <SettingLLMPrice />
                    <SettingAPIKey />
                    <SettingWebhook />
                </div>
            </div>
        </div>
    );
}
