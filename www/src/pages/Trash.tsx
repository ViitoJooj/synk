import { useCallback, useEffect, useState } from "react";
import { Button, GlassCard, Modal, Notice } from "../components/ui";
import { useI18n } from "../i18n";
import { useNow } from "../hooks/useAgent";
import { call, type SynkError } from "../services/control";
import { since, size } from "../services/format";
import type { TrashItem } from "../services/types";

export function Trash() {
    const { t } = useI18n();
    const now = useNow();
    const [items, setItems] = useState<TrashItem[] | null>(null);
    const [error, setError] = useState<SynkError | null>(null);
    const [taken, setTaken] = useState<TrashItem | null>(null);

    const load = useCallback(async () => {
        try {
            setItems(await call<TrashItem[]>("trash", { folder: "" }));
        } catch (e) {
            setError(e as SynkError);
        }
    }, []);

    useEffect(() => {
        load();
    }, [load]);

    const restore = async (item: TrashItem, force = false) => {
        setError(null);
        try {
            await call("trash.restore", { id: item.id, force });
            setTaken(null);
            await load();
        } catch (e) {
            const err = e as SynkError;
            // Um arquivo ocupa o lugar agora: pergunta antes de pôr por cima.
            if (!force && err.detail?.includes("already there")) setTaken(item);
            else setError(err);
        }
    };

    return (
        <>
            <Notice error={error} />
            <GlassCard>
                <h2>{t("trash.title")}</h2>
                <p className="dim" style={{ marginBottom: 8 }}>{t("trash.hint")}</p>
                {items?.length === 0 && <p className="empty">{t("trash.empty")}</p>}
                <div className="rows">
                    {items?.map((it) => (
                        <div className="row" key={it.id}>
                            <div className="row__main">
                                <strong className="mono">{it.path}</strong>
                                <p className="dim">
                                    {it.folder} · {size(it.size)} · {t("trash.ago", { t: since(it.trashed, now) })}
                                </p>
                            </div>
                            <Button size="sm" icon="restore" onClick={() => restore(it)}>
                                {t("trash.restore")}
                            </Button>
                        </div>
                    ))}
                </div>
            </GlassCard>
            {taken && (
                <Modal
                    title={t("trash.takenTitle")}
                    onClose={() => setTaken(null)}
                    footer={
                        <>
                            <Button variant="ghost" onClick={() => setTaken(null)}>{t("common.cancel")}</Button>
                            <Button variant="primary" onClick={() => restore(taken, true)}>{t("trash.restoreAnyway")}</Button>
                        </>
                    }
                >
                    <p>{t("trash.takenBody", { path: taken.path })}</p>
                </Modal>
            )}
        </>
    );
}
