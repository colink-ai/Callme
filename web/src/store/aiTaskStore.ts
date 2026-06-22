import { create } from 'zustand';

export type AITaskStatus = 'running' | 'succeeded' | 'failed';

export interface AITask {
  id: string;
  title: string;
  source: string;
  status: AITaskStatus;
  content: string;
  error?: string;
  createdAt: string;
  updatedAt: string;
}

interface AITaskState {
  tasks: AITask[];
  activeTaskId?: string;
  panelOpen: boolean;
  startTask: (input: { id?: string; title: string; source: string }) => string;
  appendTask: (id: string, delta: string) => void;
  setTaskContent: (id: string, content: string) => void;
  finishTask: (id: string) => void;
  failTask: (id: string, error: string) => void;
  removeTask: (id: string) => void;
  clearFinishedTasks: () => void;
  setActiveTask: (id: string) => void;
  setPanelOpen: (open: boolean) => void;
}

function newTaskID() {
  return `ai-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

function now() {
  return new Date().toISOString();
}

export const useAITaskStore = create<AITaskState>((set) => ({
  tasks: [],
  panelOpen: false,
  startTask: (input) => {
    const id = input.id || newTaskID();
    const task: AITask = {
      id,
      title: input.title,
      source: input.source,
      status: 'running',
      content: '',
      createdAt: now(),
      updatedAt: now(),
    };
    set((state) => ({
      tasks: [task, ...state.tasks.filter((item) => item.id !== id)].slice(0, 20),
      activeTaskId: id,
      panelOpen: true,
    }));
    return id;
  },
  appendTask: (id, delta) => set((state) => ({
    tasks: state.tasks.map((task) => (
      task.id === id ? { ...task, content: task.content + delta, updatedAt: now() } : task
    )),
  })),
  setTaskContent: (id, content) => set((state) => ({
    tasks: state.tasks.map((task) => (
      task.id === id ? { ...task, content, updatedAt: now() } : task
    )),
  })),
  finishTask: (id) => set((state) => ({
    tasks: state.tasks.map((task) => (
      task.id === id ? { ...task, status: 'succeeded', updatedAt: now() } : task
    )),
  })),
  failTask: (id, error) => set((state) => ({
    tasks: state.tasks.map((task) => (
      task.id === id ? { ...task, status: 'failed', error, updatedAt: now() } : task
    )),
  })),
  removeTask: (id) => set((state) => {
    const tasks = state.tasks.filter((task) => task.id !== id);
    return {
      tasks,
      activeTaskId: state.activeTaskId === id ? tasks[0]?.id : state.activeTaskId,
    };
  }),
  clearFinishedTasks: () => set((state) => {
    const tasks = state.tasks.filter((task) => task.status === 'running');
    return {
      tasks,
      activeTaskId: tasks.some((task) => task.id === state.activeTaskId) ? state.activeTaskId : tasks[0]?.id,
    };
  }),
  setActiveTask: (id) => set({ activeTaskId: id, panelOpen: true }),
  setPanelOpen: (open) => set({ panelOpen: open }),
}));
