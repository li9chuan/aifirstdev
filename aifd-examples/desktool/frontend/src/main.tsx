import { StrictMode, useState } from "react";
import { createRoot } from "react-dom/client";
import { Alert, Button, Card, ConfigProvider, Form, Input, Layout, Menu, Statistic, Table, Tag, Typography } from "antd";
import "antd/dist/reset.css";
import "./styles.css";
import { selectBackend, withStartupTimeout } from "./backend-mode";

const { Header, Sider, Content } = Layout;
const { Title, Text, Paragraph } = Typography;

import type {
  BootstrapData, WorkspaceData, SessionState, LoginRequest, LoginResult,
  UpdateInfo, DownloadResult, UpdateResult,
} from "../bindings/github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/desktop/models";
import type { ActivityItem, NavItem } from "../bindings/github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/business/models";
type DesktopBackend = {
  Bootstrap(): PromiseLike<BootstrapData>;
  Login(req: LoginRequest): PromiseLike<LoginResult>;
  LoadWorkspace(): PromiseLike<WorkspaceData>;
  CurrentSession(): PromiseLike<SessionState>;
  Logout(): PromiseLike<BootstrapData>;
  CheckUpdate(): PromiseLike<UpdateInfo>;
  DownloadUpdate(): PromiseLike<DownloadResult>;
  InstallUpdate(): PromiseLike<UpdateResult>;
};

const emptySession: SessionState = {
  authenticated: false,
  accountID: "",
  uid: 0,
  username: "",
  displayName: "",
  role: "",
};

let fallbackSession: SessionState = emptySession;

const fallbackBackend: DesktopBackend = {
  async Bootstrap() {
    return buildBootstrap(fallbackSession, "公开基础数据已加载，可选择是否登录");
  },
  async Login(req: LoginRequest) {
    const ok = req.username.trim() === "demo" && req.password.trim() === "demo123";
    const session = ok
      ? {
          authenticated: true,
          accountID: "preview-demo",
          uid: 1001,
          username: "demo",
          displayName: "Demo User",
          role: "Workspace Admin",
        }
      : emptySession;
    fallbackSession = session;
    return {
      ok,
      message: ok ? "登录成功" : "账号或密码不正确",
      session,
      bootstrap: buildBootstrap(emptySession, ok ? "" : "账号或密码不正确"),
      workspace: ok ? buildWorkspace(session, "登录成功") : buildWorkspace(emptySession, "请先登录后查看工作台数据"),
    };
  },
  async LoadWorkspace() {
    return buildWorkspace(
      fallbackSession,
      fallbackSession.authenticated ? "工作台已刷新" : "请先登录后查看工作台数据",
    );
  },
  async CurrentSession() {
    return fallbackSession;
  },
  async Logout() {
    fallbackSession = emptySession;
    return buildBootstrap(emptySession, "已退出登录");
  },
  async CheckUpdate() {
    return {
      configured: false,
      available: false,
      unsupported: false,
      invalid: false,
      currentVersion: "0.1.0",
      version: "",
      minVersion: "",
      force: false,
      url: "",
      sha256: "",
      notes: [],
      message: "未配置更新源",
    };
  },
  async DownloadUpdate() {
    return {
      ok: false,
      file: "",
      bytes: 0,
      update: await fallbackBackend.CheckUpdate(),
      message: "没有可下载的更新",
    };
  },
  async InstallUpdate() {
    return {
      ok: false,
      unsupported: false,
      message: "请先下载更新包",
    };
  },
};

function App({ backend, initialBootstrap }: { backend: DesktopBackend; initialBootstrap: BootstrapData }) {
  const [bootstrap, setBootstrap] = useState<BootstrapData>(initialBootstrap);
  const [workspace, setWorkspace] = useState<WorkspaceData | null>(null);
  const [session, setSession] = useState<SessionState>(initialBootstrap.session);
  const [loginMessage, setLoginMessage] = useState("");
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null);
  const [downloadResult, setDownloadResult] = useState<DownloadResult | null>(null);
  const [installResult, setInstallResult] = useState<UpdateResult | null>(null);
  const [updateBusy, setUpdateBusy] = useState(false);

  async function login(values: LoginRequest) {
    const result = await backend.Login(values);
    setLoginMessage(result.message);
    setSession(result.session);
    if (result.ok) {
      setWorkspace(result.workspace);
      return;
    }
    setWorkspace(null);
    setBootstrap(result.bootstrap);
  }

  async function logout() {
    const data = await backend.Logout();
    setBootstrap(data);
    setWorkspace(null);
    setSession(data.session);
    setLoginMessage(data.message);
  }

  async function refreshWorkspace() {
    const data = await backend.LoadWorkspace();
    setWorkspace(data);
  }

  async function checkUpdate() {
    setUpdateBusy(true);
    try {
      const info = await backend.CheckUpdate();
      setUpdateInfo(info);
      setDownloadResult(null);
      setInstallResult(null);
    } finally {
      setUpdateBusy(false);
    }
  }

  async function downloadUpdate() {
    setUpdateBusy(true);
    try {
      const result = await backend.DownloadUpdate();
      setDownloadResult(result);
      setUpdateInfo(result.update);
      setInstallResult(null);
    } finally {
      setUpdateBusy(false);
    }
  }

  async function installUpdate() {
    setUpdateBusy(true);
    try {
      const result = await backend.InstallUpdate();
      setInstallResult(result);
    } finally {
      setUpdateBusy(false);
    }
  }

  const activeNav = (bootstrap.nav ?? defaultNav()).map((item, index) => ({
    key: String(index),
    label: item.label,
  }));

  return (
    <ConfigProvider
      theme={{
        token: {
          colorPrimary: "#2f6f73",
          borderRadius: 6,
          fontFamily:
            'Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
        },
      }}
    >
      <Layout className="desk-shell">
        <Sider width={248} className="desk-sidebar">
          <div className="brand">
            <div className="brand-mark">D</div>
            <div>
              <div className="brand-title">{bootstrap.appTitle || "DeskTool"}</div>
              <div className="brand-subtitle">{bootstrap.mode || "AI-first Desktop"}</div>
            </div>
          </div>
          <Menu className="nav-menu" mode="inline" selectedKeys={["0"]} items={activeNav} />
          <div className="side-footer">
            <Text className="muted-label">运行模式</Text>
            <Tag color="gold">Wails Local</Tag>
          </div>
        </Sider>

        <Layout>
          <Header className="desk-topbar">
            <div className="desk-title">
              <Title level={3}>{workspace?.title || bootstrap.title || "桌面工作台"}</Title>
              <Paragraph>{session.authenticated ? `当前用户：${session.displayName}` : "未登录"}</Paragraph>
            </div>
            <div className="session-actions">
              <Tag color={session.authenticated ? "green" : "default"}>
                {session.authenticated ? session.role : "Guest"}
              </Tag>
            </div>
          </Header>

          <Content className="desk-content">
            <div className="content-grid">
              <main className="main-panel">
                {session.authenticated && workspace ? (
                  <WorkspacePanel data={workspace} onLogout={logout} onRefresh={refreshWorkspace} />
                ) : (
                  <PublicPanel bootstrap={bootstrap} loginMessage={loginMessage} onLogin={login} />
                )}
              </main>
              <aside className="side-stack">
                <UpdatePanel
                  busy={updateBusy}
                  info={updateInfo}
                  download={downloadResult}
                  install={installResult}
                  onCheck={checkUpdate}
                  onDownload={downloadUpdate}
                  onInstall={installUpdate}
                />
                <ActivityPanel items={workspace?.activity ?? bootstrap.activity ?? []} />
              </aside>
            </div>
          </Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}

function PublicPanel(props: {
  bootstrap: BootstrapData;
  loginMessage: string;
  onLogin: (values: LoginRequest) => PromiseLike<void>;
}) {
  return (
    <div className="public-layout">
      <Card className="summary-card">
        <Title level={4}>{props.bootstrap.summary}</Title>
        <Paragraph>{props.bootstrap.message}</Paragraph>
        <Alert
          showIcon
          type="info"
          title="开发期 AI-first"
          description="Ant Design MCP / CLI / LLMs.txt 用于 Codex 开发期查询组件和最佳实践；运行时只消费普通业务数据。"
        />
      </Card>
      <Card className="login-card" title="登录 DeskTool">
        <Form<LoginRequest>
          layout="vertical"
          initialValues={{ username: "demo", password: "demo123" }}
          onFinish={(values) => void props.onLogin(values)}
        >
          <Form.Item label="账号" name="username" rules={[{ required: true, message: "请输入账号" }]}>
            <Input autoComplete="username" />
          </Form.Item>
          <Form.Item label="密码" name="password" rules={[{ required: true, message: "请输入密码" }]}>
            <Input.Password autoComplete="current-password" />
          </Form.Item>
          <Button block type="primary" htmlType="submit">
            登录
          </Button>
        </Form>
        {props.loginMessage ? <Text className="login-message">{props.loginMessage}</Text> : null}
      </Card>
    </div>
  );
}

function UpdatePanel(props: {
  busy: boolean;
  info: UpdateInfo | null;
  download: DownloadResult | null;
  install: UpdateResult | null;
  onCheck: () => PromiseLike<void>;
  onDownload: () => PromiseLike<void>;
  onInstall: () => PromiseLike<void>;
}) {
  const message = props.install?.message || props.download?.message || props.info?.message || "Windows 使用 NSIS 整包更新。";
  const downloadedBytes = props.download?.ok ? formatBytes(props.download.bytes) : "";
  return (
    <Card className="update-panel" title="Update">
      <div className="update-stack">
        <Text>{message}</Text>
        {props.info && !props.info.configured ? <Text className="muted-label">设置 DESKTOOL_UPDATE_MANIFEST_URL 后可检查远端清单。</Text> : null}
        {props.info?.invalid ? <Alert showIcon type="error" title="更新清单无效" description={props.info.message} /> : null}
        {props.info?.available ? (
          <Tag color={props.info.force ? "red" : "blue"}>
            {props.info.force ? "强制更新" : `新版本 ${props.info.version}`}
          </Tag>
        ) : (
          <Tag color="default">{props.info?.currentVersion ? `当前 ${props.info.currentVersion}` : "未检查"}</Tag>
        )}
        {props.info?.notes?.length ? (
          <ul className="update-notes">
            {props.info.notes.map((note) => (
              <li key={note}>{note}</li>
            ))}
          </ul>
        ) : null}
        {props.info?.available ? (
          <div className="update-meta">
            <Text className="muted-label">当前版本</Text>
            <Text>{props.info.currentVersion || "-"}</Text>
            <Text className="muted-label">目标版本</Text>
            <Text>{props.info.version || "-"}</Text>
            {downloadedBytes ? (
              <>
                <Text className="muted-label">已下载</Text>
                <Text>{downloadedBytes}</Text>
              </>
            ) : null}
          </div>
        ) : null}
        <div className="update-actions">
          <Button htmlType="button" loading={props.busy} onClick={() => void props.onCheck()}>
            检查更新
          </Button>
          <Button
            htmlType="button"
            disabled={!props.info?.available || props.info.invalid}
            loading={props.busy}
            onClick={() => void props.onDownload()}
          >
            下载
          </Button>
          <Button
            htmlType="button"
            disabled={!props.download?.ok}
            loading={props.busy}
            onClick={() => void props.onInstall()}
          >
            安装
          </Button>
        </div>
        {props.download?.ok ? <Text className="muted-label">安装会启动 Windows 安装器，请按提示关闭或覆盖当前应用。</Text> : null}
      </div>
    </Card>
  );
}

function WorkspacePanel(props: {
  data: WorkspaceData;
  onLogout: () => PromiseLike<void>;
  onRefresh: () => PromiseLike<void>;
}) {
  return (
    <div className="workspace-panel">
      <Card>
        <div className="workspace-heading">
          <div>
            <Title level={4}>{props.data.summary}</Title>
            <Paragraph>{props.data.message}</Paragraph>
          </div>
          <div className="workspace-actions">
            <Button htmlType="button" onClick={() => void props.onRefresh()}>
              刷新
            </Button>
            <button
              className="logout-button"
              data-testid="logout-button"
              type="button"
              onClick={() => void props.onLogout()}
            >
              退出登录
            </button>
          </div>
        </div>
      </Card>
      <div className="metric-row">
        {(props.data.metrics ?? []).map((metric) => (
          <Card key={metric.label}>
            <Statistic title={metric.label} value={metric.value} />
          </Card>
        ))}
      </div>
      <Table
        rowKey="name"
        pagination={false}
        dataSource={props.data.tasks ?? []}
        columns={[
          { title: "名称", dataIndex: "name" },
          { title: "状态", dataIndex: "status" },
          { title: "更新时间", dataIndex: "time" },
        ]}
      />
    </div>
  );
}

function ActivityPanel(props: { items: ActivityItem[] }) {
  return (
    <Card className="activity-panel" title="Activity">
      <div className="activity-list">
        {props.items.map((item, index) => (
          <div className="activity-item" key={`${item.text}-${index}`}>
            <Text>{item.text}</Text>
          </div>
        ))}
      </div>
    </Card>
  );
}

function buildBootstrap(session: SessionState, message: string): BootstrapData {
  return {
    appTitle: "DeskTool",
    mode: "AI-first Desktop",
    title: "桌面工作台",
    summary: "这是普通 Wails + React + Ant Design 渲染的公开主界面。",
    message,
    session,
    nav: defaultNav(),
    activity: [{ text: "Fallback frontend initialized" }, { text: message }],
  };
}

function buildWorkspace(session: SessionState, message: string): WorkspaceData {
  return {
    title: "桌面工作台",
    summary: session.authenticated ? `${session.displayName} 的示例工作台。` : "请先登录后查看工作台数据",
    message,
    session,
    metrics: [
      { label: "Tasks", value: "12" },
      { label: "Reports", value: "4" },
      { label: "Alerts", value: "0" },
    ],
    tasks: [
      { name: "示例任务 A", status: "Ready", time: "09:30" },
      { name: "示例报告 B", status: "Draft", time: "10:15" },
    ],
    activity: [{ text: "Fallback workspace loaded" }, { text: message }],
  };
}

function defaultNav(): NavItem[] {
  return [
    { label: "概览", active: true },
    { label: "工作区", active: false },
    { label: "报告", active: false },
    { label: "设置", active: false },
  ];
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return "0 B";
  }
  const units = ["B", "KB", "MB", "GB"];
  let value = bytes;
  let index = 0;
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024;
    index += 1;
  }
  return `${value.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

const root = document.querySelector("#app");
if (!root) {
  throw new Error("app root missing");
}

const view = createRoot(root);
void selectBackend<DesktopBackend>(
  import.meta.env.MODE,
  () => import("../bindings/github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/desktop/desktopapp"),
  fallbackBackend,
).then(async (backend) => {
  const initialBootstrap = await withStartupTimeout(backend.Bootstrap());
  view.render(<StrictMode><App backend={backend} initialBootstrap={initialBootstrap} /></StrictMode>);
}).catch((error: unknown) => {
  console.error("Desktop backend startup failed", error);
  view.render(<Alert type="error" showIcon title="桌面服务连接失败" description="请重新启动应用；若问题持续，请检查后端服务。" />);
});
