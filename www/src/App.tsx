import { useState } from "react";
import { Sidebar, type Page } from "./components/Sidebar/Sidebar";
import { AgentDown } from "./components/AgentDown";
import { useAgent } from "./hooks/useAgent";
import { Overview } from "./pages/Overview";
import { Peers } from "./pages/Peers";
import { Folders } from "./pages/Folders";
import { Settings } from "./pages/Settings";
import { Logs } from "./pages/Logs";
import "./App.scss";

export function App() {
    const status = useAgent();
    const [page, setPage] = useState<Page>("overview");

    return (
        <div className="app">
            <Sidebar page={page} onPage={setPage} status={status} />
            <main className="app__main">
                {status === null && <AgentDown />}
                {page === "overview" && <Overview status={status} onPage={setPage} />}
                {page === "peers" && <Peers />}
                {page === "folders" && <Folders status={status} />}
                {page === "settings" && <Settings />}
                {page === "logs" && <Logs />}
            </main>
        </div>
    );
}
