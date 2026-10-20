import type { InputHTMLAttributes, SelectHTMLAttributes } from "react";
import "./Input.scss";

export function Input({ className = "", ...rest }: InputHTMLAttributes<HTMLInputElement>) {
    return <input className={`sk-input ${className}`} {...rest} />;
}

export function Select({ className = "", children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
    return (
        <select className={`sk-input sk-select ${className}`} {...rest}>
            {children}
        </select>
    );
}
