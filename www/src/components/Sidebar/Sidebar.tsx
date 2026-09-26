import { Badge, Icon, type IconName } from "../ui";
import { useI18n } from "../../i18n";
import type { AgentStatus } from "../../services/types";
import "./Sidebar.scss";

export type Page = "overview" | "peers" | "folders" | "settings" | "logs";

const items: { page: Page; icon: IconName }[] = [
    { page: "overview", icon: "link" },
    { page: "peers", icon: "monitor" },
    { page: "folders", icon: "folder" },
    { page: "settings", icon: "sliders" },
    { page: "logs", icon: "list" },
];

export function Sidebar({ page, onPage, status }: { page: Page; onPage: (p: Page) => void; status: AgentStatus | null | undefined }) {
    const { t } = useI18n();
    const up = status?.link.state === "connected";

    return (
        <nav className="sidebar">
            <div className="sidebar__brand">
                <span className="sidebar__logo" />
                synk
            </div>
            <div className="sidebar__items">
                {items.map((it) => (
                    <button key={it.page} className={page === it.page ? "on" : ""} onClick={() => onPage(it.page)}>
                        <Icon name={it.icon} />
                        {t(`nav.${it.page}`)}
                    </button>
                ))}
            </div>
            <div className="sidebar__foot">
                {status === undefined ? null : status === null ? (
                    <Badge tone="failed" dot>{t("agent.stopped")}</Badge>
                ) : (
                    <Badge tone={up ? "success" : "pending"} dot>
                        {up ? t("link.up") : t("link.waiting")}
                    </Badge>
                )}
            </div>
        </nav>
    );
}
