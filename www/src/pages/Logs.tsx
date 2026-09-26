import { useEffect, useRef, useState } from "react";
import { GlassCard, Notice } from "../components/ui";
import { useI18n } from "../i18n";
import { call, type SynkError } from "../services/control";
import "./Logs.scss";

export function Logs() {
    const { t } = useI18n();
    const [lines, setLines] = useState<string[]>([]);
    const [error, setError] = useState<SynkError | null>(null);
    const box = useRef<HTMLDivElement>(null);
    const pinned = useRef(true);

    useEffect(() => {
        let alive = true;
        const load = () =>
            call<string[]>("logs", { lines: 400 })
                .then((l) => {
                    if (!alive) return;
                    setLines(l);
                    setError(null);
                })
                .catch((e) => alive && setError(e as SynkError));
        load();
        const id = setInterval(load, 2000);
        return () => {
            alive = false;
            clearInterval(id);
        };
    }, []);

    // Segue o fim enquanto o usuário não rolou para cima.
    useEffect(() => {
        const el = box.current;
        if (el && pinned.current) el.scrollTop = el.scrollHeight;
    }, [lines]);

    return (
        <>
            <div className="page-head">
                <div>
                    <h1>{t("nav.logs")}</h1>
                    <p className="dim">{t("logs.subtitle")}</p>
                </div>
            </div>
            <Notice error={error} />
            <GlassCard className="logs">
                <div
                    className="logs__box mono"
                    ref={box}
                    onScroll={(e) => {
                        const el = e.currentTarget;
                        pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
                    }}
                >
                    {lines.length === 0 && <p className="dim">{t("logs.empty")}</p>}
                    {lines.map((l, i) => (
                        <div key={i} className={/fail|error|refused|cannot|down/i.test(l) ? "logs__bad" : ""}>
                            {l}
                        </div>
                    ))}
                </div>
            </GlassCard>
        </>
    );
}
