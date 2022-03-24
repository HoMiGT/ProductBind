# ProductBind 

    该项目是产线自动化绑定


## Toml配置文件

    config.toml 是程序启动的一些动态参数 需要重启程序
    rule.toml 是程序读取摄像头的一些参数配置 不需要重启程序

## Go文件
     
    main.go 程序的入口 
    controllers.go  程序逻辑
    models.go  程序的结构体及其方法
    util.go  程序依赖的一些函数

## Txt文件 
    
    log.txt 程序日志文件

## Mp3 
    
    tips.mp3 程序提示`未识别标签`
    warn.mp3 程序提示`识别版数不对`

## Sqlite
    
    records.sqlite 程序读取摄像头的记录

## Static 
    
    static 文件夹，放置读取的成功，失败以及上传失败的csv文件
    
    
## windows部署程序为服务 

    1 部署服务需要俩个软件 instsrv.exe和srvany.exe
    2 打开windows终端 输入如下命令
        绝对路径\\instsrv.exe ProductAutoBind 绝对路径\\srvany.exe
    说明：绝对路径是存放软件的绝对路径，ProductAtuoBind是服务的名称，可自定义，当前项目的名称为ProductAutoBind
    3 win+R打开运行，输入regedit,打开注册表编辑器
    4 在注册表 
        HKEY_LOCAL_MACHINE  
            SYSTEM
                CurrentControlSet
                    Services
      下找到刚刚注册的服务名ProductAutoBind,右键新建一个项，名为"Parameters",选中该项并右键新建三个字符串分别为"Application","AppDirectory","Description"
    并分别右键三个字符串设置值，如下图所示:
![Image](https://github.com/TeslaHou/ProductBind/blob/master/iShot2022-03-24%2009.44.35.png?raw=true)
<!-- <img src="./iShot2022-03-24%09.44.35.png"> -->
    
    5 右键我的电脑选择"管理"进入到windows服务中启动服务如下图所示:
<img src="./iShot2022-03-24%09.52.34.png">
      
      更新程序需要先在此停止服务，对服务的启动，暂停以及重启均在此设置，设置之后程序将随系统自动启动。
      

### 特别说明:
    
    rule.toml详细配置说明:
    目前程序仅支持单箱码与多数码的模式
    摄像头返回多个箱码，程序将通过","来进行拼接。
    其他详细说明请详见rule.toml的文件内的备注
    

