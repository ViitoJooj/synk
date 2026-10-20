import { useEffect, type ReactNode } from "react";
import "./Modal.scss";

export interface ModalProps {
    title: string;
    onClose: () => void;
    children: ReactNode;
    footer?: ReactNode;
    wide?: boolean;
}

export function Modal({ title, onClose, children, footer, wide }: ModalProps) {
    useEffect(() => {
        const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
        window.addEventListener("keydown", onKey);
        return () => window.removeEventListener("keydown", onKey);
    }, [onClose]);

    return (
        <div className="sk-modal" onMouseDown={onClose}>
            <div className={`sk-modal__panel ${wide ? "sk-modal__panel--wide" : ""}`} onMouseDown={(e) => e.stopPropagation()}>
                <h2>{title}</h2>
                <div className="sk-modal__body">{children}</div>
                {footer && <div className="sk-modal__footer">{footer}</div>}
            </div>
        </div>
    );
}
