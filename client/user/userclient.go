package user

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/dispatcher/handlers/filters"
	"github.com/celestix/gotgproto/ext"
	"github.com/celestix/gotgproto/sessionMaker"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/client/middleware"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
)

var (
	clientMu sync.RWMutex
	loginMu  sync.Mutex
	uc       *gotgproto.Client
	ectx     *ext.Context
)

var logoutClient = func(ctx context.Context, client *gotgproto.Client) error {
	if _, err := client.API().AuthLogOut(ctx); err != nil {
		return err
	}
	client.Stop()
	return nil
}

func GetCtx() *ext.Context {
	clientMu.RLock()
	defer clientMu.RUnlock()
	return ectx
}

func Login(ctx context.Context) (*gotgproto.Client, error) {
	loginMu.Lock()
	defer loginMu.Unlock()

	log.FromContext(ctx).Debug("Logging in user client")
	clientMu.RLock()
	client := uc
	clientMu.RUnlock()
	if client != nil {
		return client, nil
	}
	type loginResult struct {
		client *gotgproto.Client
		err    error
	}
	result := make(chan loginResult)
	go func() {
		client, err := Authenticate(ctx, &terminalAuthConversator{})
		select {
		case result <- loginResult{client: client, err: err}:
		case <-ctx.Done():
			if client != nil {
				client.Stop()
			}
		}
	}()

	var login loginResult
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case login = <-result:
	}
	if login.err != nil {
		if login.client != nil {
			login.client.Stop()
		}
		return nil, login.err
	}
	client = login.client
	client.Dispatcher.AddHandler(handlers.NewMessage(filters.Message.All, dispatchRelayMessage))
	client.Dispatcher.AddHandler(handlers.NewMessage(filters.Message.Media, func(ctx *ext.Context, u *ext.Update) error {
		switch u.UpdateClass.(type) {
		case *tg.UpdateEditChannelMessage, *tg.UpdateEditMessage, *tg.UpdateDeleteChannelMessages, *tg.UpdateDeleteMessages:
			return dispatcher.EndGroups
		}
		chatId := u.EffectiveChat().GetID()
		watchChats, err := database.GetWatchChatsByChatID(ctx, chatId)
		if err != nil || len(watchChats) == 0 {
			return dispatcher.EndGroups
		}
		return dispatcher.ContinueGroups
	}))
	client.Dispatcher.AddHandler(handlers.NewMessage(filters.Message.Media, handleMediaMessage))
	clientCtx := client.CreateContext()
	clientMu.Lock()
	uc = client
	ectx = clientCtx
	clientMu.Unlock()
	log.FromContext(ctx).Infof("User client logged in successfully: %s", client.Self.FirstName+" "+client.Self.LastName)
	return client, nil
}

// Logout 注销 Telegram 授权并停止当前用户客户端。
func Logout(ctx context.Context) error {
	loginMu.Lock()
	defer loginMu.Unlock()

	clientMu.RLock()
	client := uc
	clientMu.RUnlock()
	if client == nil {
		return nil
	}
	if err := logoutClient(ctx, client); err != nil {
		return fmt.Errorf("log out user client: %w", err)
	}
	clientMu.Lock()
	if uc == client {
		uc = nil
		ectx = nil
	}
	clientMu.Unlock()
	return nil
}

// Authenticate 使用指定的认证交互器登录用户账号，供非终端登录流程复用。
func Authenticate(ctx context.Context, conversator gotgproto.AuthConversator) (*gotgproto.Client, error) {
	if conversator == nil {
		return nil, fmt.Errorf("user auth conversator is required")
	}
	resolver, err := tgutil.NewConfigProxyResolver()
	if err != nil {
		return nil, fmt.Errorf("create user proxy resolver: %w", err)
	}
	client, err := gotgproto.NewClient(
		config.C().Telegram.AppID,
		config.C().Telegram.AppHash,
		gotgproto.ClientTypePhone(""),
		&gotgproto.ClientOpts{
			Session:          sessionMaker.SqlSession(database.GetDialect(config.C().Telegram.Userbot.Session)),
			AuthConversator:  conversator,
			Context:          ctx,
			DisableCopyright: true,
			Resolver:         resolver,
			MaxRetries:       config.C().Telegram.RpcRetry,
			AutoFetchReply:   true,
			Middlewares:      middleware.NewDefaultMiddlewares(ctx, 5*time.Minute),
			ErrorHandler: func(ctx *ext.Context, u *ext.Update, s string) error {
				log.FromContext(ctx).Errorf("Unhandled error: %s", s)
				return dispatcher.EndGroups
			},
		},
	)
	if err != nil {
		return client, fmt.Errorf("create user client: %w", err)
	}
	return client, nil
}
