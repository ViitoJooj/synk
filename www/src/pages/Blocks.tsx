import { useCallback, useEffect, useState } from "react";
import { Badge, Button, GlassCard, Input, Notice } from "../components/ui";
import { useI18n } from "../i18n";
import { call, type SynkError } from "../services/control";
import type { BlockInfo } from "../services/types";

export function Blocks() {
    const { t } = useI18n();
    const [blocks, setBlocks] = useState<BlockInfo[]>([]);
    const [what, setWhat] = useState("");
    const [error, setError] = useState<SynkError | null>(null);

    const load = useCallback(async () => {
        try {
            setBlocks(await call<BlockInfo[]>("blocks"));
        } catch (e) {
            setError(e as SynkError);
        }
    }, []);

    useEffect(() => {
        load();
    }, [load]);

    const run = async (verb: string, target: string) => {
        setError(null);
        try {
            await call(verb, { what: target });
            setWhat("");
            await load();
        } catch (e) {
            setError(e as SynkError);
        }
    };

    const mine = blocks.filter((b) => b.origin !== "built-in");
    const builtIn = blocks.filter((b) => b.origin === "built-in");

    return (
        <>
            <Notice error={error} />
            <GlassCard>
                <h2>{t("blocks.title")}</h2>
                <p className="dim" style={{ marginBottom: 12 }}>{t("blocks.hint")}</p>
                <form
                    style={{ display: "flex", gap: 8 }}
                    onSubmit={(e) => {
                        e.preventDefault();
                        if (what.trim()) run("blocks.add", what.trim());
                    }}
                >
                    <Input style={{ flex: 1 }} value={what} placeholder={t("blocks.placeholder")} onChange={(e) => setWhat(e.target.value)} />
                    <Button variant="primary" icon="shield" type="submit" disabled={!what.trim()}>
                        {t("blocks.add")}
                    </Button>
                </form>
                <div className="rows" style={{ marginTop: 8 }}>
                    {mine.map((b) => (
                        <div className="row" key={b.what}>
                            <div className="row__main mono">{b.what}</div>
                            <Badge tone={b.origin === "default" ? "idle" : "review"}>{t(`blocks.origin.${b.origin}`)}</Badge>
                            <Button size="sm" variant="ghost" icon="x" onClick={() => run("blocks.rm", b.what)} />
                        </div>
                    ))}
                </div>
            </GlassCard>
            <GlassCard>
                <h2>{t("blocks.builtIn")}</h2>
                <p className="dim" style={{ marginBottom: 8 }}>{t("blocks.builtInHint")}</p>
                <div className="mono dim">
                    {builtIn.map((b) => (
                        <div key={b.what}>{b.what}</div>
                    ))}
                </div>
            </GlassCard>
        </>
    );
}
