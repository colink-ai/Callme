import { expect, test } from '@playwright/test';

test('user can register, start a chat, and receive a streamed agent answer', async ({ page }) => {
  const username = `e2e_${Date.now()}`;

  await page.goto('/chat');
  await page.getByText('注册').click();
  await page.getByLabel('用户名').fill(username);
  await page.getByLabel('密码').fill('pass1234');
  await page.getByRole('button', { name: /注\s*册/ }).click();

  await expect(page.getByRole('heading', { name: '智能问答' })).toBeVisible();
  await expect(page.getByText(username)).toBeVisible();

  const startButton = page.getByRole('button', { name: '开始会话' });
  if (await startButton.isVisible()) {
    await startButton.click();
  }

  const input = page.getByPlaceholder(/输入您的问题/);
  await expect(input).toBeVisible();
  await input.fill('你好，请介绍一下 Callme');
  await page.getByRole('button', { name: '发送' }).click();

  await expect(page.locator('.chat-bubble.user').filter({ hasText: '你好，请介绍一下 Callme' })).toBeVisible();
  await expect(page.getByText(/mock ACP agent 的回答/)).toBeVisible();
  await expect(page.getByText(/代码知识图谱/)).toBeVisible();
});
