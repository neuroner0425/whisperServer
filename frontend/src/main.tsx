import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { Navigate, RouterProvider, createBrowserRouter } from 'react-router-dom'

import { AppShell } from './AppShell'
import { AuthPage } from './features/auth/AuthPage'
import { FilesPage } from './features/files/FilesPage'
import { JobDetailPage } from './features/jobs/JobDetailPage'
import { StoragePage } from './features/storage/StoragePage'
import { TrashPage } from './features/trash/TrashPage'
import './styles.css'

const router = createBrowserRouter([
  {
    path: '/auth/login',
    element: <AuthPage mode="login" />,
  },
  {
    path: '/auth/join',
    element: <AuthPage mode="signup" />,
  },
  {
    path: '/',
    element: <AppShell />,
    children: [
      { path: '/', element: <Navigate replace to="/files/home" /> },
      { path: '/files/home', element: <FilesPage key="files-home" viewMode="home" /> },
      { path: '/files/root', element: <FilesPage key="files-root" viewMode="explore" /> },
      { path: '/files/folder/:folderId', element: <FilesPage key="files-folder" viewMode="explore" /> },
      { path: '/files/search', element: <FilesPage key="files-search" viewMode="search" /> },
      { path: '/files/trash', element: <TrashPage /> },
      { path: '/files/storage', element: <StoragePage /> },
      { path: '/file/:jobId', element: <JobDetailPage /> },
    ],
  },
  {
    path: '*',
    element: <Navigate replace to="/files/home" />,
  },
])

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <RouterProvider router={router} />
  </StrictMode>,
)
