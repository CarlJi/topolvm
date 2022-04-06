package lvmd

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/topolvm/topolvm/lvmd/command"
	"github.com/topolvm/topolvm/lvmd/proto"
	"google.golang.org/grpc/metadata"
)

type mockWatchServer struct {
	ch  chan struct{}
	ctx context.Context
}

func (s *mockWatchServer) Send(r *proto.WatchResponse) error {
	s.ch <- struct{}{}
	return nil
}

func (s *mockWatchServer) SetHeader(metadata.MD) error {
	panic("implement me")
}

func (s *mockWatchServer) SendHeader(metadata.MD) error {
	panic("implement me")
}

func (s *mockWatchServer) SetTrailer(metadata.MD) {
	panic("implement me")
}

func (s *mockWatchServer) Context() context.Context {
	return s.ctx
}

func (s *mockWatchServer) SendMsg(m interface{}) error {
	panic("implement me")
}

func (s *mockWatchServer) RecvMsg(m interface{}) error {
	panic("implement me")
}

func testWatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	vgService, notifier := NewVGService(NewDeviceClassManager([]*DeviceClass{{Name: "ssd", VolumeGroup: "test_vgservice"}}))

	ch1 := make(chan struct{})
	server1 := &mockWatchServer{
		ctx: ctx,
		ch:  ch1,
	}
	done := make(chan struct{})
	go func() {
		vgService.Watch(&proto.Empty{}, server1)
		done <- struct{}{}
	}()

	select {
	case <-ch1:
	case <-time.After(1 * time.Second):
		t.Fatal("not received the first event")
	}

	notifier()

	select {
	case <-ch1:
	case <-time.After(1 * time.Second):
		t.Fatal("not received")
	}

	select {
	case <-ch1:
		t.Fatal("unexpected event")
	default:
	}

	ch2 := make(chan struct{})
	server2 := &mockWatchServer{
		ctx: ctx,
		ch:  ch2,
	}
	go func() {
		vgService.Watch(&proto.Empty{}, server2)
	}()

	notifier()

	select {
	case <-ch1:
	case <-time.After(1 * time.Second):
		t.Fatal("not received")
	}
	select {
	case <-ch2:
	case <-time.After(1 * time.Second):
		t.Fatal("not received")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("not done")
	}
}

func testVGService(t *testing.T, vg *command.VolumeGroup) {
	spareGB := uint64(1)
	vgService, _ := NewVGService(NewDeviceClassManager([]*DeviceClass{{Name: vg.Name(), VolumeGroup: vg.Name(), SpareGB: &spareGB}}))
	res, err := vgService.GetLVList(context.Background(), &proto.GetLVListRequest{DeviceClass: vg.Name()})
	if err != nil {
		t.Fatal(err)
	}
	numVols1 := len(res.GetVolumes())
	if numVols1 != 0 {
		t.Errorf("numVolumes must be 0: %d", numVols1)
	}
	testtag := "testtag"
	_, err = vg.CreateVolume("test1", 1<<30, []string{testtag}, 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	res, err = vgService.GetLVList(context.Background(), &proto.GetLVListRequest{DeviceClass: vg.Name()})
	if err != nil {
		t.Fatal(err)
	}
	numVols2 := len(res.GetVolumes())
	if numVols2 != 1 {
		t.Fatalf("numVolumes must be 1: %d", numVols2)
	}

	vol := res.GetVolumes()[0]
	if vol.GetName() != "test1" {
		t.Errorf(`Volume.Name != "test1": %s`, vol.GetName())
	}
	if vol.GetSizeGb() != 1 {
		t.Errorf(`Volume.SizeGb != 1: %d`, vol.GetSizeGb())
	}
	if len(vol.GetTags()) != 1 {
		t.Fatalf("number of tags must be 1")
	}
	if vol.GetTags()[0] != testtag {
		t.Errorf(`Volume.Tags[0] != %s: %v`, testtag, vol.GetTags())
	}

	_, err = vg.CreateVolume("test2", 1<<30, nil, 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = exec.Command("lvresize", "-L", "+12m", vg.Name()+"/test1").Run()
	if err != nil {
		t.Fatal(err)
	}

	res, err = vgService.GetLVList(context.Background(), &proto.GetLVListRequest{DeviceClass: vg.Name()})
	if err != nil {
		t.Fatal(err)
	}
	numVols3 := len(res.GetVolumes())
	if numVols3 != 2 {
		t.Fatalf("numVolumes must be 2: %d", numVols3)
	}

	res2, err := vgService.GetFreeBytes(context.Background(), &proto.GetFreeBytesRequest{DeviceClass: vg.Name()})
	if err != nil {
		t.Fatal(err)
	}
	freeBytes, err := vg.Free()
	if err != nil {
		t.Fatal(err)
	}
	expected := freeBytes - (1 << 30)
	if res2.GetFreeBytes() != expected {
		t.Errorf("Free bytes mismatch: %d, expected: %d, freeBytes: %d", res2.GetFreeBytes(), expected, freeBytes)
	}

	test3Vol, err := vg.CreateVolume("test3", 1<<30, nil, 2, "4k", nil)
	if err != nil {
		t.Fatal(err)
	}

	test4Vol, err := vg.CreateVolume("test4", 1<<30, nil, 2, "4M", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Remove volumes to make room for a raid volume
	test3Vol.Remove()
	test4Vol.Remove()

	_, err = vg.CreateVolume("test5", 1<<30, nil, 0, "", []string{"--type=raid1"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestVGService(t *testing.T) {
	// uid := os.Getuid()
	// if uid != 0 {
	// 	t.Skip("run as root")
	// }

	vgName := "test_vgservice"
	loop1, err := MakeLoopbackDevice(vgName + "1")
	if err != nil {
		t.Fatal(err)
	}
	loop2, err := MakeLoopbackDevice(vgName + "2")
	if err != nil {
		t.Fatal(err)
	}

	err = MakeLoopbackVG(vgName, loop1, loop2)
	if err != nil {
		t.Fatal(err)
	}
	defer CleanLoopbackVG(vgName, []string{loop1, loop2}, []string{vgName + "1", vgName + "2"})

	vg, err := command.FindVolumeGroup(vgName)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("VGService", func(t *testing.T) {
		testVGService(t, vg)
	})
	t.Run("Watch", func(t *testing.T) {
		testWatch(t)
	})
}
