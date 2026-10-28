import { createContext, useContext, type ParentComponent } from 'solid-js';
import { createSignal, createEffect, on } from 'solid-js';
import { useServer } from './server';

export interface Notification {
  id: string;
  type: 'task.started' | 'task.completed' | 'task.failed';
  taskTitle: string;
  taskId: string;
  timestamp: number;
  read: boolean;
}

interface NotificationContextValue {
  notifications: () => Notification[];
  unreadCount: () => number;
  markRead: (id: string) => void;
  markAllRead: () => void;
  clear: () => void;
}

const NotificationContext = createContext<NotificationContextValue>();

export const NotificationProvider: ParentComponent = (props) => {
  const server = useServer();
  const [notifications, setNotifications] = createSignal<Notification[]>([]);
  const unreadCount = () => notifications().filter((n) => !n.read).length;

  const markRead = (id: string) => {
    setNotifications((prev) => prev.map((n) => (n.id === id ? { ...n, read: true } : n)));
  };

  const markAllRead = () => {
    setNotifications((prev) => prev.map((n) => ({ ...n, read: true })));
  };

  const clear = () => setNotifications([]);

  // Listen for task state change SSE events
  createEffect(on(server.eventTick, () => {
    const last = server.lastEvent();
    if (!last) return;
    if (last.type !== 'task.started' && last.type !== 'task.completed' && last.type !== 'task.failed') return;

    const props = last.properties as Record<string, any> | undefined;
    if (!props?.id) return;

    const notif: Notification = {
      id: `notif-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
      type: last.type as Notification['type'],
      taskTitle: props.title || props.id,
      taskId: props.id,
      timestamp: Date.now(),
      read: false,
    };

    setNotifications((prev) => [notif, ...prev].slice(0, 50));
  }));

  const value: NotificationContextValue = {
    notifications,
    unreadCount,
    markRead,
    markAllRead,
    clear,
  };

  return (
    <NotificationContext.Provider value={value}>
      {props.children}
    </NotificationContext.Provider>
  );
};

export function useNotifications() {
  const ctx = useContext(NotificationContext);
  if (!ctx) throw new Error('useNotifications must be used within NotificationProvider');
  return ctx;
}
