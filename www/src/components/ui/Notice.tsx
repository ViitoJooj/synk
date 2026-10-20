import type { ReactNode } from "react";
import { Icon } from "./Icon";
import type { SynkError } from "../../services/control";
import "./Notice.scss";

// Notice é uma faixa de aviso: um erro do agente com a dica dele, ou um
// recado. O código snk-NN vai junto, como na linha de comando.
export function Notice({ error, children, tone = "failed" }: { error?: SynkError | null; children?: ReactNode; tone?: "failed" | "pending" | "progress" }) {
    if (!error && !children) return null;
    return (
        <div className={`sk-notice sk-notice--${tone}`}>
            <Icon name="alert" size={16} />
            <div>
                {error ? (
                    <>
                        <strong>{error.message}</strong>
                        {error.hint && <p>{error.hint}</p>}
                        {error.detail && <p className="faint mono">{error.detail}</p>}
                        <p className="faint mono">{error.code}</p>
                    </>
                ) : (
                    children
                )}
            </div>
        </div>
    );
}
